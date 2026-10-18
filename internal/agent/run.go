package agent

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"slices"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/dockerapi"
	"github.com/ldesfontaine/opencloud/internal/dockerwatch"
	"github.com/ldesfontaine/opencloud/internal/hostinfo"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/registry"
	"github.com/ldesfontaine/opencloud/internal/resource"
	"github.com/ldesfontaine/opencloud/internal/sampler"
	"github.com/ldesfontaine/opencloud/internal/update"
)

// ErrIdentityRefused : le serveur ne reconnaît plus cette machine ; seul un
// nouvel enrôlement la ramène.
var ErrIdentityRefused = errors.New("identity refused by the server")

const (
	DefaultSignalInterval = 30 * time.Second
	minBackoff            = time.Second
	maxBackoff            = time.Minute
	backoffJitter         = 0.25
	// Un flux qui a tenu ce temps remet le délai de reconnexion au plus court.
	stableStream = 30 * time.Second
)

type Options struct {
	StateDir *os.Root
	// Server et Token ne servent qu'au premier démarrage, pour s'enrôler.
	Server string
	Token  string
	Pin    string
	// AllowPlain accepte http:// vers un serveur distant ; à réserver à un
	// réseau déjà chiffré.
	AllowPlain bool
	// Language est la langue de l'opérateur, donnée par la commande
	// d'installation ; l'identité la garde ensuite.
	Language lang.Code
	// Version est celle du binaire ; le serveur l'affiche.
	Version        string
	SignalInterval time.Duration
	// SampleInterval est la cadence de mesure ; zéro vaut celle du produit.
	SampleInterval time.Duration
	// DockerSocket est la socket du démon ; vide vaut celle de Docker.
	DockerSocket string
	// Roots est le magasin contre lequel les sondes de cette machine
	// vérifient une chaîne. nil vaut le magasin du système : c'est la
	// machine qui sonde qui juge, avec les autorités qu'elle connaît.
	Roots  *x509.CertPool
	Logger *slog.Logger
}

// Run enrôle l'agent si besoin, puis tient le flux ouvert jusqu'à ce que le
// contexte s'arrête ou que le serveur refuse l'identité pour de bon.
func Run(ctx context.Context, opts Options) error {
	if opts.SignalInterval <= 0 {
		opts.SignalInterval = DefaultSignalInterval
	}
	if opts.SampleInterval <= 0 {
		opts.SampleInterval = resource.SampleInterval
	}
	identity, err := loadOrEnroll(ctx, opts)
	if err != nil {
		return err
	}
	client, err := NewClient(identity.Server, identity.Pin, opts.Version, opts.AllowPlain)
	if err != nil {
		return err
	}
	// Ce que l'opérateur doit régler sans que l'agent s'arrête s'écrit au
	// journal dans sa langue.
	catalogs, err := lang.Load()
	if err != nil {
		return err
	}
	if opts.DockerSocket == "" {
		opts.DockerSocket = dockerapi.DefaultSocket
	}
	// La mesure continue pendant une coupure : le tampon rattrape au retour.
	readings := &buffer{}
	go sampleLoop(ctx, sampler.New(), readings, opts)
	// Le veilleur Docker aussi ; ses rapports attendent dans le leur.
	observed := &reports{}
	docker := dockerapi.New(opts.DockerSocket)
	watcher := dockerwatch.New(docker, observed, dockerwatch.Options{Logger: opts.Logger})
	go watcher.Run(ctx)
	// Les images de la machine se vérifient contre leur registre, avec le
	// trousseau Docker de la machine ; les constats attendent aussi.
	constated := &images{}
	updater := update.NewRunner(docker, update.NewChecker(docker, registry.New(registry.File{Path: registry.DefaultConfigPath()})), constated, opts.Logger)
	go updater.Run(ctx)
	// Les sondes tournent tant que l'agent vit : leur jeu vient du serveur,
	// mais une coupure du flux ne les arrête pas, le tampon garde les essais.
	checked := &results{}
	prober := probe.NewRunner(checked, probe.NewChecker(opts.Roots), opts.Logger)
	go prober.Run(ctx)
	return keepConnected(ctx, client, identity, &pending{readings: readings, reports: observed, results: checked, images: constated, logs: watcher, probes: prober, updates: updater}, catalogs.For(identity.Language), opts)
}

// pending est ce que l'agent a à livrer, et ce qui sait servir les
// journaux, les sondes et les vérifications d'images quand le serveur
// les demande.
type pending struct {
	readings *buffer
	reports  *reports
	results  *results
	images   *images
	logs     container.LogSource
	probes   probe.Assignable
	updates  update.Assignable
}

func sampleLoop(ctx context.Context, probe *sampler.Sampler, readings *buffer, opts Options) {
	ticker := time.NewTicker(opts.SampleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			reading, ok, err := probe.Sample(now)
			if err != nil {
				opts.Logger.Warn("sample machine", "error", err)
				continue
			}
			if ok {
				readings.push(reading)
			}
		}
	}
}

func loadOrEnroll(ctx context.Context, opts Options) (Identity, error) {
	identity, err := LoadIdentity(opts.StateDir)
	if err == nil {
		opts.Logger.Info("identity loaded", "machine_id", identity.MachineID, "server", identity.Server)
		return identity, nil
	}
	if !errors.Is(err, ErrNotEnrolled) {
		return Identity{}, err
	}
	if opts.Server == "" || opts.Token == "" {
		return Identity{}, ErrNotEnrolled
	}
	return enroll(ctx, opts)
}

// enroll tire l'identité, la présente avec le jeton, puis l'écrit avec l'id
// que le serveur a retenu : un ré-enrôlement garde l'id de la machine.
func enroll(ctx context.Context, opts Options) (Identity, error) {
	identity, err := NewIdentity(opts.Server, opts.Pin, opts.Language)
	if err != nil {
		return Identity{}, err
	}
	client, err := NewClient(identity.Server, identity.Pin, opts.Version, opts.AllowPlain)
	if err != nil {
		return Identity{}, err
	}
	response, err := client.Enroll(ctx, identity, opts.Token, hostinfo.Collect())
	if err != nil {
		return Identity{}, err
	}
	identity.MachineID = response.MachineID
	identity.EnrolledAt = time.Now().UTC()
	if err := SaveIdentity(opts.StateDir, identity); err != nil {
		return Identity{}, err
	}
	opts.Logger.Info("enrolled", "machine_id", identity.MachineID, "name", response.Name)
	return identity, nil
}

func keepConnected(ctx context.Context, client *Client, identity Identity, pending *pending, text lang.Catalog, opts Options) error {
	delay := minBackoff
	for {
		startedAt := time.Now()
		err := connectOnce(ctx, client, identity, pending, opts)
		if ctx.Err() != nil {
			return nil
		}
		var refused *ServerError
		isRefused := errors.As(err, &refused)
		if isRefused && refused.Permanent() {
			return fmt.Errorf("%w: %w", ErrIdentityRefused, err)
		}
		// Le serveur a oublié la session, en redémarrant le plus souvent : un
		// nouveau défi suffit. Pas de boucle serrée, une session ne se perd
		// qu'au signal, un intervalle après l'ouverture du flux.
		if isRefused && refused.Code == codeNoSession {
			opts.Logger.Info("session expired, reconnecting now")
			continue
		}
		if time.Since(startedAt) > stableStream {
			delay = minBackoff
		}
		// L'horloge décalée ne se règle pas seule : l'opérateur doit lire
		// pourquoi la machine reste hors ligne, l'agent continue d'essayer.
		if isRefused && refused.Code == codeClockSkew {
			message, _ := Explain(err, text)
			opts.Logger.Warn("clock skew, reconnecting", "message", message, "delay", delay.Round(time.Millisecond))
		} else {
			opts.Logger.Warn("stream lost, reconnecting", "error", err, "delay", delay.Round(time.Millisecond))
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(jittered(delay)):
		}
		delay = min(delay*2, maxBackoff)
	}
}

// connectOnce tient un flux et envoie un signal à intervalle régulier tant
// qu'il vit ; il rend la main dès que le flux tombe. Ce qui attendait dans
// le tampon des conteneurs est marqué rejoué : le serveur l'écrit sans y
// voir du neuf.
func connectOnce(ctx context.Context, client *Client, identity Identity, pending *pending, opts Options) error {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := client.OpenStream(streamCtx, identity)
	if err != nil {
		return err
	}
	defer stream.Close()
	opts.Logger.Info("connected", "server", identity.Server)
	pending.reports.markReplayed()
	pending.results.markReplayed()
	pending.images.markReplayed()

	commands := newCommandRunner(streamCtx, client, stream.Session, pending.logs, pending.probes, pending.updates, opts.Logger)
	lost := make(chan error, 1)
	go func() { lost <- stream.Follow(commands.handle) }()

	ticker := time.NewTicker(opts.SignalInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-lost:
			return err
		case <-ticker.C:
			if err := signal(ctx, client, stream.Session, pending, opts.Logger); err != nil {
				return err
			}
		}
	}
}

// signal livre le tampon par lots, tant qu'il reste de quoi faire un lot
// plein : un rattrapage se vide en quelques signaux serrés. Une erreur
// réseau remet le lot dans le tampon. Une section refusée est jetée, elle
// ne passera jamais ; les autres sont écrites et ne repartent pas. Les
// essais des sondes partent dans le même corps, à côté des mesures et des
// conteneurs.
func signal(ctx context.Context, client *Client, session string, pending *pending, logger *slog.Logger) error {
	for {
		sent := signalBatch{
			readings:   pending.readings.take(resource.MaxReadingsPerSignal),
			containers: pending.reports.take(),
			probes:     pending.results.take(),
			images:     pending.images.take(),
		}
		rejected, err := client.Signal(ctx, session, sent.readings, sent.containers, sent.probes, sent.images)
		var refused *ServerError
		switch {
		case errors.As(err, &refused) && refused.Status == http.StatusBadRequest:
			// Un serveur plus vieux s'arrête à la première section refusée :
			// celles d'avant sont écrites, celles d'après ne l'ont pas été.
			logger.Warn("signal refused", "code", refused.Code)
			sent.restoreAfter(pending, refusedSection(refused.Code))
		case err != nil:
			sent.restoreAfter(pending, "")
			return err
		}
		for section, code := range rejected {
			logger.Warn("signal section refused", "section", section, "code", code)
		}
		if len(sent.readings) < resource.MaxReadingsPerSignal {
			return nil
		}
	}
}

// Ce qu'un signal emporte, section par section.
type signalBatch struct {
	readings   []sampler.Reading
	containers *container.Report
	probes     *probe.Report
	images     *update.Report
}

// restoreAfter remet dans le tampon les sections qui suivent celle
// nommée, dans l'ordre où le serveur les écrit ; sans nom, tout le lot.
// Une clé inconnue ne dit pas où le serveur s'est arrêté : rien ne revient.
func (b signalBatch) restoreAfter(pending *pending, refused string) {
	isUnwritten := refused == ""
	if !isUnwritten && !slices.Contains(machine.SignalSections(), refused) {
		return
	}
	for _, section := range machine.SignalSections() {
		if isUnwritten {
			b.restore(pending, section)
		}
		if section == refused {
			isUnwritten = true
		}
	}
}

func (b signalBatch) restore(pending *pending, section string) {
	switch section {
	case machine.SectionReadings:
		pending.readings.restore(b.readings)
	case machine.SectionServices:
		pending.reports.restore(b.containers)
	case machine.SectionProbes:
		pending.results.restore(b.probes)
	case machine.SectionImages:
		pending.images.restore(b.images)
	}
}

// refusedSection retrouve la section d'une clé de refus de l'ancien serveur.
func refusedSection(code string) string {
	for _, section := range machine.SignalSections() {
		if machine.SectionRefusal(section) == code {
			return section
		}
	}
	return code
}

func jittered(delay time.Duration) time.Duration {
	spread := float64(delay) * backoffJitter
	offset := (rand.Float64()*2 - 1) * spread // #nosec G404 -- un délai de reconnexion, pas un secret.
	return delay + time.Duration(offset)
}
