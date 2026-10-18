package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/auth"
	"github.com/ldesfontaine/opencloud/internal/config"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/dockerapi"
	"github.com/ldesfontaine/opencloud/internal/dockerwatch"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/hostinfo"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/lifecycle"
	"github.com/ldesfontaine/opencloud/internal/live"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/mail"
	"github.com/ldesfontaine/opencloud/internal/manifest"
	"github.com/ldesfontaine/opencloud/internal/mcp"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/registry"
	"github.com/ldesfontaine/opencloud/internal/resource"
	"github.com/ldesfontaine/opencloud/internal/sampler"
	"github.com/ldesfontaine/opencloud/internal/server"
	"github.com/ldesfontaine/opencloud/internal/settings"
	"github.com/ldesfontaine/opencloud/internal/status"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/trust"
	"github.com/ldesfontaine/opencloud/internal/update"
	"github.com/ldesfontaine/opencloud/internal/version"
)

const (
	readHeaderTimeout = 10 * time.Second
	// Une connexion gardée ouverte sans requête se ferme au bout de deux
	// minutes ; un flux en direct n'est pas concerné, il a une requête.
	idleTimeout     = 2 * time.Minute
	shutdownTimeout = 10 * time.Second
	stateDirMode    = 0o700
)

func runServe(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	configPath := flags.String("config", config.DefaultPath, "chemin du fichier de configuration")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, warnings, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.SlogLevel()}))
	for _, warning := range warnings {
		logger.Warn("config", "detail", warning)
	}
	// Les autorités avant tout le reste : un chemin faux doit arrêter le
	// démarrage, jamais laisser les sondes juger sur un magasin vide.
	roots, err := trust.Load(cfg.CAFile)
	if err != nil {
		return err
	}
	stateDir, err := openStateDir(cfg.StateDir)
	if err != nil {
		return err
	}
	defer stateDir.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(ctx, stateDir)
	if err != nil {
		return err
	}
	defer db.Close()
	// La clé avant tout ce qui lit un secret, et avant la reprise des
	// réglages, qui en écrit un.
	if err := openSecrets(ctx, stateDir, db, logger, true); err != nil {
		return err
	}
	if err := db.PurgeExpiredTokens(ctx, time.Now()); err != nil {
		logger.Warn("purge expired tokens", "error", err)
	}
	if err := importLegacySettings(ctx, stateDir, db, logger); err != nil {
		return err
	}

	// Le bus du direct : machine et heartbeat y publient, les onglets y lisent.
	bus := live.New()
	machines := machine.New(db, machine.NewSessions(), logger)
	machines.SetListener(bus)
	info := hostinfo.Collect()
	machines.SetLocalFingerprint(info.HostFingerprint)
	if err := machines.EnsureLocal(ctx, machine.LocalInfo{
		Hostname: info.Hostname,
		Address:  info.Address,
		OS:       info.OS,
		Arch:     info.Arch,
		Version:  version.Number(),
	}); err != nil {
		return err
	}

	heartbeats := heartbeat.New(db, logger)
	heartbeats.SetWatcher(bus)
	resources := resource.New(db, logger)
	resources.SetWatcher(bus)
	containers := container.New(db, logger)
	containers.SetWatcher(bus)
	// La machine openCloud veille son propre Docker, sans passer par le
	// réseau : ce que le veilleur observe s'écrit directement.
	watcher := dockerwatch.New(dockerapi.New(cfg.DockerSocket), localSink{ctx: ctx, containers: containers, logger: logger}, dockerwatch.Options{Logger: logger})
	containers.SetLocalLogSource(machine.LocalID, watcher)
	probes := probe.New(db, logger)
	probes.SetWatcher(bus)
	declared := directory.New(db, logger)
	declared.SetWatcher(bus)
	// La machine openCloud exécute aussi ses propres sondes, sans passer
	// par le réseau : son jeu ne sort pas du processus.
	prober := probe.NewRunner(localProbes{ctx: ctx, probes: probes, logger: logger}, probe.NewChecker(roots), logger)
	probes.SetLocalRunner(machine.LocalID, prober)
	// Et vérifie ses propres images contre leur registre, avec son
	// trousseau Docker ; les constats s'écrivent sans passer par le réseau.
	updates := update.New(db, logger)
	updates.SetWatcher(bus)
	// L'annuaire lit ce que les machines montrent ; le suivi des conteneurs lui
	// demande les unités à chaque rapport, et l'annuaire lui demande de
	// rattacher de nouveau après chaque écriture.
	declared.SetObserved(containers, updates)
	declared.SetRematcher(containers)
	// La politique de mise à jour vit sur l'unité : l'annuaire dit au suivi
	// des images de redire à la machine ce qu'elle ne doit plus interroger.
	declared.SetAssigner(updates)
	containers.SetUnits(declared)
	docker := dockerapi.New(cfg.DockerSocket)
	updater := update.NewRunner(docker, update.NewChecker(docker, registry.New(registry.File{Path: registry.DefaultConfigPath()})), localImages{ctx: ctx, updates: updates, logger: logger}, logger)
	updates.SetLocalRunner(machine.LocalID, updater)
	// La page de statut publique : elle écoute le bus interne pour relire,
	// et publie sur les deux bus quand quelque chose de visible change.
	publicBus := live.New()
	statusPage := status.New(db, machines, db, logger)
	statusPage.AddWatcher(bus)
	statusPage.AddWatcher(publicBus)
	statusPage.SetFeed(bus)
	// Les alertes : chaque composant lui donne ses faits, la page de statut
	// lui dit ce qui est sous maintenance, le notifieur livre aux canaux
	// dans la langue de l'interface.
	catalogs, err := lang.Load()
	if err != nil {
		return fmt.Errorf("load languages: %w", err)
	}
	alerts := alert.New(db, logger)
	alerts.SetWatcher(bus)
	alerts.SetMaintenance(statusPage)
	alerts.SetDiskThresholds(diskThresholdsOf(db, logger))
	notifier := alert.NewNotifier(db, catalogs, languageOf(db), logger)
	notifier.SetPublicURL(cfg.PublicURL)
	notifier.SetMail(mailRelayOf(db), mail.NewSender(alert.MailTimeout))
	alerts.SetSender(notifier)
	machines.SetAlerter(alerts)
	heartbeats.SetListener(alerts)
	resources.SetAlerter(alerts)
	containers.SetAlerter(alerts)
	probes.SetAlerter(alerts)
	// L'accès d'un agent IA : le client OAuth, ses jetons, leur purge.
	access := mcp.New(db, logger)
	// L'accès de l'opérateur à l'interface : relu à chaque requête.
	operator, err := auth.New(db, logger)
	if err != nil {
		return err
	}
	warnIfOpen(ctx, logger, operator, cfg.Listen)
	// La fin de support : activée tant que l'opérateur ne la coupe pas sous
	// Paramètres. Cette machine lit endoflife.date une fois par jour, deux
	// au plus, pour les produits des images présentes et les systèmes des
	// machines.
	support := lifecycle.New(containers.Images, machines.Systems, lifecycle.NewFileCache(stateDir), lifecycle.NewHTTPSource(), lifecycleEnabledOf(db, logger), logger)
	support.SetWatcher(bus)
	// Quinze boucles de fond : les échéances, le rollup et la purge des
	// mesures, la mesure de cette machine, qui est son propre agent, la
	// purge des conteneurs, le veilleur Docker, le rollup et la purge des
	// sondes, leur moteur, la purge des constats d'images et leur
	// vérificateur, la page de statut, les machines perdues, le moteur des
	// alertes, le notifieur, la purge des jetons MCP et la fin de support.
	// Elles finissent avant que la base ne se ferme.
	var loops sync.WaitGroup
	runLoop(&loops, func() { heartbeats.Watch(ctx) })
	runLoop(&loops, func() { resources.Watch(ctx) })
	runLoop(&loops, func() { resources.SampleLocal(ctx, machine.LocalID, sampler.New(), resource.SampleInterval) })
	runLoop(&loops, func() { containers.Watch(ctx) })
	runLoop(&loops, func() { watcher.Run(ctx) })
	runLoop(&loops, func() { probes.Watch(ctx) })
	runLoop(&loops, func() { prober.Run(ctx) })
	runLoop(&loops, func() { updates.Watch(ctx) })
	runLoop(&loops, func() { updater.Run(ctx) })
	runLoop(&loops, func() { statusPage.Watch(ctx) })
	runLoop(&loops, func() { machines.Watch(ctx) })
	runLoop(&loops, func() { alerts.Watch(ctx) })
	runLoop(&loops, func() { notifier.Run(ctx) })
	runLoop(&loops, func() { access.Watch(ctx) })
	runLoop(&loops, func() { support.Watch(ctx) })
	defer func() { stop(); loops.Wait() }()
	// Le moteur local ne sait rien tant qu'on ne lui a rien donné : ce que
	// la base garde des sondes de cette machine repart dès le démarrage.
	probes.Assign(ctx, machine.LocalID)
	// Les services saisis avant que le rapprochement existe, ou pendant que
	// le serveur était arrêté, sont rattachés sans attendre l'inventaire
	// suivant de chaque agent. Les exclusions se disent après : c'est
	// l'unité rattachée qui porte la politique.
	containers.RematchAll(ctx)
	updates.Assign(ctx, machine.LocalID)
	logServicesToReview(ctx, declared, logger)

	// Le fichier de l'annuaire lit et écrit par les mêmes composants que
	// l'interface : les agents reçoivent leurs sondes, le rapprochement se
	// refait, les alertes suivent. Son arrêt suit celui du serveur : un apply
	// en cours finit son changement puis se journalise « interrompu ».
	manifests := manifest.New(manifest.Dependencies{
		Directory: declared, Probes: probes, Heartbeats: heartbeats, Machines: db, History: db, Journal: db,
	}, logger)
	manifests.SetErrorKeys(server.RefusalKey)
	manifests.SetWatcher(bus)
	manifests.SetLifetime(ctx)
	// Un passage resté « en cours » est celui d'un arrêt brutal : plus rien
	// ne l'écrit, il passe en échec pour que le journal dise vrai.
	if _, err := manifests.FailInterrupted(ctx); err != nil {
		logger.Warn("mark interrupted applies", "error", err)
	}

	server, err := server.New(server.Options{
		Logger:         logger,
		Version:        version.Number(),
		Settings:       db,
		Machines:       machines,
		Heartbeats:     heartbeats,
		Resources:      resources,
		Containers:     containers,
		Probes:         probes,
		Directory:      declared,
		Manifest:       manifests,
		Updates:        updates,
		Lifecycle:      support,
		Status:         statusPage,
		Alerts:         alerts,
		Notifier:       notifier,
		MCP:            access,
		Auth:           operator,
		Live:           bus,
		PublicLive:     publicBus,
		PublicURL:      cfg.PublicURL,
		Listen:         cfg.Listen,
		TrustedProxies: cfg.TrustedPrefixes(),
		ConfigPath:     *configPath,
	})
	if err != nil {
		return err
	}
	// Le serveur tient les flux des agents : c'est lui qui leur commande.
	containers.SetCommander(server)
	probes.SetCommander(server)
	updates.SetCommander(server)
	// Le port principal s'ouvre avant la socket de commande : un second serve
	// sur le même state_dir échoue ici, avant d'avoir touché à la socket du
	// premier.
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("serve: %w", err)
	}
	control := startControlSocket(logger, stateDir, cfg.StateDir, server.ControlHandler())
	return listenUntilSignal(ctx, logger, newHTTPServer(cfg.Listen, server), listener, control, cfg)
}

// importLegacySettings reprend settings.toml, où vivaient les réglages avant
// la base, puis l'efface. Un fichier illisible arrête le démarrage ; un
// fichier qui ne s'efface pas est seulement signalé, la base a les réglages.
func importLegacySettings(ctx context.Context, stateDir *os.Root, db *store.DB, logger *slog.Logger) error {
	outcome, err := settings.ImportLegacyFile(ctx, stateDir, db)
	if errors.Is(err, settings.ErrLegacyFileKept) {
		logger.Warn("legacy settings file kept", "file", "settings.toml", "imported", outcome == settings.Imported, "error", err)
		return nil
	}
	if err != nil {
		return err
	}
	switch outcome {
	case settings.Imported:
		logger.Info("settings imported into the database", "file", "settings.toml")
	case settings.AlreadyInDatabase:
		// Un ancien binaire a pu réécrire le fichier entre-temps : ce qu'il y
		// a changé est perdu, l'opérateur doit le savoir.
		logger.Warn("legacy settings file removed without applying it, the database already had settings", "file", "settings.toml")
	}
	return nil
}

// Ce que les relectures attendent de la base : les réglages courants. Les
// relectures ci-dessous servent des composants qui demandent une simple
// fonction ; elles ne suivent pas l'arrêt du processus, pour qu'une alerte
// partie pendant l'arrêt garde sa langue et son relais.
type settingsReader interface {
	LoadSettings(ctx context.Context) (settings.Settings, error)
}

// languageOf relit la langue de l'interface au moment d'envoyer : le
// canal reçoit ce que l'opérateur lit.
func languageOf(preferences settingsReader) func() lang.Code {
	return func() lang.Code {
		current, err := preferences.LoadSettings(context.Background())
		if err != nil || current.Language == "" {
			return lang.Default
		}
		return current.Language
	}
}

// mailRelayOf relit le relais au moment d'envoyer : un réglage changé
// sous Paramètres vaut dès l'alerte suivante. Une lecture en échec laisse
// le relais vide, et l'envoi le dit par son motif.
func mailRelayOf(preferences settingsReader) func() mail.Relay {
	return func() mail.Relay {
		current, err := preferences.LoadSettings(context.Background())
		if err != nil {
			return mail.Relay{}
		}
		return current.MailRelay()
	}
}

// diskThresholdsOf relit les réglages à chaque appel : un seuil changé
// sous Paramètres vaut à la lecture suivante. Une lecture en échec garde
// l'alerte en marche, aux seuils par défaut.
func diskThresholdsOf(preferences settingsReader, logger *slog.Logger) func() alert.DiskThresholds {
	return func() alert.DiskThresholds {
		current, err := preferences.LoadSettings(context.Background())
		if err != nil {
			logger.Warn("read disk thresholds", "error", err)
			return alert.DefaultDiskThresholds()
		}
		return alert.DiskThresholdsOr(current.DiskAttentionPercent, current.DiskDangerPercent)
	}
}

// lifecycleEnabledOf relit les réglages à chaque appel : la fin de support
// coupée ou rallumée sous Paramètres vaut aussitôt. Une lecture en échec la
// laisse coupée : on ne sait pas si l'opérateur l'a coupée.
func lifecycleEnabledOf(preferences settingsReader, logger *slog.Logger) func() bool {
	return func() bool {
		current, err := preferences.LoadSettings(context.Background())
		if err != nil {
			logger.Warn("read lifecycle setting", "error", err)
			return false
		}
		return current.LifecycleEnabled()
	}
}

// warnIfOpen dit au journal que l'interface n'est pas protégée, plus fort
// quand elle écoute ailleurs que sur la boucle locale.
func warnIfOpen(ctx context.Context, logger *slog.Logger, operator *auth.Service, listen string) {
	state, err := operator.State(ctx)
	if err != nil {
		logger.Warn("read interface authentication", "error", err)
		return
	}
	if state.Mode != auth.ModeNone {
		logger.Info("interface authentication", "mode", string(state.Mode), "username", state.Username, "header", state.Header)
		return
	}
	if isLoopbackListen(listen) {
		logger.Warn("interface authentication disabled: anyone reaching the port is the operator; enable it under Settings", "listen", listen)
		return
	}
	logger.Warn("INTERFACE OPEN TO THE NETWORK WITHOUT AUTHENTICATION: anyone reaching this address can enroll machines, read logs and enable MCP; enable it under Settings now",
		"listen", listen)
}

// isLoopbackListen : « 127.0.0.1:8080 », « [::1]:8080 », « localhost:8080 ».
// Une adresse vide (« :8080 ») écoute partout.
func isLoopbackListen(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	address, err := netip.ParseAddr(host)
	return err == nil && address.IsLoopback()
}

func runLoop(loops *sync.WaitGroup, loop func()) {
	loops.Add(1)
	go func() {
		defer loops.Done()
		loop()
	}()
}

// Le répertoire d'état est ouvert une fois par os.Root ; les composants qui
// y écrivent passent par lui.
func openStateDir(path string) (*os.Root, error) {
	if err := os.MkdirAll(path, stateDirMode); err != nil { // bounded: config.StateDir
		return nil, fmt.Errorf("create state dir: %w", err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, fmt.Errorf("open state dir: %w", err)
	}
	return root, nil
}

// newHTTPServer prépare le serveur HTTP. Les flux en direct (agents,
// interface, journaux, statut, MCP) ne finissent qu'avec le contexte de
// leur requête : il dérive d'un contexte de base annulé dès l'arrêt, sans
// quoi Shutdown les attendrait jusqu'à son délai. Une requête courte en
// cours au même instant voit aussi son contexte annulé : sa transaction
// est annulée, l'agent ou le navigateur la rejoue.
//
// Pas de ReadTimeout : net/http annule le contexte d'une requête dont la
// lecture expire, même une fois le corps lu, ce qui couperait tous les
// flux. Un corps lent reste borné en taille (1 Mio) et Traefik, devant,
// tient ses propres délais de lecture.
func newHTTPServer(addr string, handler http.Handler) *http.Server {
	base, cancelBase := context.WithCancel(context.Background())
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
		BaseContext:       func(net.Listener) context.Context { return base },
	}
	httpServer.RegisterOnShutdown(cancelBase)
	return httpServer
}

// listenUntilSignal sert sur listener jusqu'au signal d'arrêt. control, la
// socket de commande, est nil quand elle n'a pas pu s'ouvrir.
func listenUntilSignal(ctx context.Context, logger *slog.Logger, httpServer *http.Server, listener net.Listener, control *http.Server, cfg config.Config) error {
	if control != nil {
		// Fermer retire aussi le fichier de la socket, quelle que soit la
		// sortie.
		defer control.Close()
	}
	failed := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.Listen, "state_dir", cfg.StateDir, "version", version.String())
		failed <- httpServer.Serve(listener)
	}()

	select {
	case err := <-failed:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	// La socket de commande s'arrête en même temps que le serveur principal,
	// pas avant lui : une requête de commande lente ne doit pas manger le
	// délai du reste.
	controlDone := make(chan error, 1)
	if control != nil {
		go func() { controlDone <- control.Shutdown(shutdownCtx) }()
	} else {
		controlDone <- nil
	}
	mainErr := httpServer.Shutdown(shutdownCtx)
	if err := <-controlDone; err != nil {
		logger.Warn("shutdown control socket", "error", err)
	}
	if mainErr != nil && !errors.Is(mainErr, http.ErrServerClosed) {
		return fmt.Errorf("shutdown: %w", mainErr)
	}
	return nil
}

// localSink écrit ce que le veilleur de la machine openCloud observe.
type localSink struct {
	ctx        context.Context
	containers *container.Tracker
	logger     *slog.Logger
}

func (s localSink) Deliver(report container.Report) {
	if err := s.containers.Record(s.ctx, machine.LocalID, report); err != nil && s.ctx.Err() == nil {
		s.logger.Error("record local containers", "error", err)
	}
}

// localImages écrit ce que la machine openCloud a constaté sur ses images.
type localImages struct {
	ctx     context.Context
	updates *update.Service
	logger  *slog.Logger
}

func (l localImages) Deliver(report update.Report) {
	if err := l.updates.Record(l.ctx, machine.LocalID, report); err != nil && l.ctx.Err() == nil {
		l.logger.Error("record local image checks", "error", err)
	}
}

// localProbes écrit ce que les sondes de la machine openCloud ont donné.
type localProbes struct {
	ctx    context.Context
	probes *probe.Service
	logger *slog.Logger
}

func (p localProbes) Deliver(report probe.Report) {
	if err := p.probes.Record(p.ctx, machine.LocalID, report); err != nil && p.ctx.Err() == nil {
		p.logger.Error("record local probes", "error", err)
	}
}

// logServicesToReview dit au journal combien de services repris de l'existant
// attendent la relecture de l'opérateur : la mise à jour qui relie l'annuaire
// en crée, et rien d'autre ne le prévient.
func logServicesToReview(ctx context.Context, declared *directory.Directory, logger *slog.Logger) {
	found, err := declared.ListServices(ctx, directory.Filter{})
	if err != nil {
		logger.Warn("list declared services to review", "error", err)
		return
	}
	count := 0
	for _, candidate := range found {
		if candidate.NeedsReview {
			count++
		}
	}
	if count > 0 {
		logger.Info("declared services to review", "count", count)
	}
}
