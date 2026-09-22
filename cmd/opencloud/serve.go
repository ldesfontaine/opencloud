package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/config"
	"github.com/ldesfontaine/opencloud/internal/dockerapi"
	"github.com/ldesfontaine/opencloud/internal/dockerwatch"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/hostinfo"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/live"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/registry"
	"github.com/ldesfontaine/opencloud/internal/resource"
	"github.com/ldesfontaine/opencloud/internal/sampler"
	"github.com/ldesfontaine/opencloud/internal/server"
	"github.com/ldesfontaine/opencloud/internal/service"
	"github.com/ldesfontaine/opencloud/internal/settings"
	"github.com/ldesfontaine/opencloud/internal/status"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/trust"
	"github.com/ldesfontaine/opencloud/internal/update"
	"github.com/ldesfontaine/opencloud/internal/version"
)

const (
	readHeaderTimeout = 10 * time.Second
	shutdownTimeout   = 10 * time.Second
	stateDirMode      = 0o700
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
	if err := db.PurgeExpiredTokens(ctx, time.Now()); err != nil {
		logger.Warn("purge expired tokens", "error", err)
	}

	// Le bus du direct : machine et heartbeat y publient, les onglets y lisent.
	bus := live.New()
	machines := machine.New(db, machine.NewSessions(), logger)
	machines.SetListener(bus)
	info := hostinfo.Collect()
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
	services := service.New(db, logger)
	services.SetWatcher(bus)
	// La machine openCloud veille son propre Docker, sans passer par le
	// réseau : ce que le veilleur observe s'écrit directement.
	watcher := dockerwatch.New(dockerapi.New(cfg.DockerSocket), localSink{ctx: ctx, services: services, logger: logger}, dockerwatch.Options{Logger: logger})
	services.SetLocalLogSource(machine.LocalID, watcher)
	probes := probe.New(db, logger)
	probes.SetWatcher(bus)
	// La machine openCloud exécute aussi ses propres sondes, sans passer
	// par le réseau : son jeu ne sort pas du processus.
	prober := probe.NewRunner(localProbes{ctx: ctx, probes: probes, logger: logger}, probe.NewChecker(roots), logger)
	probes.SetLocalRunner(machine.LocalID, prober)
	// Et vérifie ses propres images contre leur registre, avec son
	// trousseau Docker ; les constats s'écrivent sans passer par le réseau.
	updates := update.New(db, logger)
	updates.SetWatcher(bus)
	docker := dockerapi.New(cfg.DockerSocket)
	updater := update.NewRunner(docker, update.NewChecker(docker, registry.New(registry.File{Path: registry.DefaultConfigPath()})), localImages{ctx: ctx, updates: updates, logger: logger}, logger)
	updates.SetLocalRunner(machine.LocalID, updater)
	// La page de statut publique : elle écoute le bus interne pour relire,
	// et publie sur les deux bus quand quelque chose de visible change.
	preferences := settings.New(stateDir)
	publicBus := live.New()
	statusPage := status.New(db, machines, preferences, logger)
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
	notifier := alert.NewNotifier(db, catalogs, languageOf(preferences), logger)
	alerts.SetSender(notifier)
	machines.SetAlerter(alerts)
	heartbeats.SetListener(alerts)
	resources.SetAlerter(alerts)
	services.SetAlerter(alerts)
	probes.SetAlerter(alerts)
	// Treize boucles de fond : les échéances, le rollup et la purge des
	// mesures, la mesure de cette machine, qui est son propre agent, la
	// purge des services, le veilleur Docker, le rollup et la purge des
	// sondes, leur moteur, la purge des constats d'images et leur
	// vérificateur, la page de statut, les machines perdues, le moteur des
	// alertes et le notifieur. Elles finissent avant que la base ne se
	// ferme.
	var loops sync.WaitGroup
	runLoop(&loops, func() { heartbeats.Watch(ctx) })
	runLoop(&loops, func() { resources.Watch(ctx) })
	runLoop(&loops, func() { resources.SampleLocal(ctx, machine.LocalID, sampler.New(), resource.SampleInterval) })
	runLoop(&loops, func() { services.Watch(ctx) })
	runLoop(&loops, func() { watcher.Run(ctx) })
	runLoop(&loops, func() { probes.Watch(ctx) })
	runLoop(&loops, func() { prober.Run(ctx) })
	runLoop(&loops, func() { updates.Watch(ctx) })
	runLoop(&loops, func() { updater.Run(ctx) })
	runLoop(&loops, func() { statusPage.Watch(ctx) })
	runLoop(&loops, func() { machines.Watch(ctx) })
	runLoop(&loops, func() { alerts.Watch(ctx) })
	runLoop(&loops, func() { notifier.Run(ctx) })
	defer func() { stop(); loops.Wait() }()
	// Le moteur local ne sait rien tant qu'on ne lui a rien donné : ce que
	// la base garde des sondes de cette machine repart dès le démarrage.
	probes.Assign(ctx, machine.LocalID)
	updates.Assign(ctx, machine.LocalID)

	server, err := server.New(server.Options{
		Logger:         logger,
		Version:        version.Number(),
		Settings:       preferences,
		Machines:       machines,
		Heartbeats:     heartbeats,
		Resources:      resources,
		Services:       services,
		Probes:         probes,
		Updates:        updates,
		Status:         statusPage,
		Alerts:         alerts,
		Notifier:       notifier,
		Live:           bus,
		PublicLive:     publicBus,
		PublicURL:      cfg.PublicURL,
		TrustedProxies: cfg.TrustedPrefixes(),
	})
	if err != nil {
		return err
	}
	// Le serveur tient les flux des agents : c'est lui qui leur commande.
	services.SetCommander(server)
	probes.SetCommander(server)
	updates.SetCommander(server)
	httpServer := &http.Server{
		Addr:              cfg.Listen,
		Handler:           server,
		ReadHeaderTimeout: readHeaderTimeout,
	}
	return listenUntilSignal(ctx, logger, httpServer, cfg)
}

// languageOf relit la langue de l'interface au moment d'envoyer : le
// canal reçoit ce que l'opérateur lit.
func languageOf(preferences *settings.Store) func() lang.Code {
	return func() lang.Code {
		current, err := preferences.Load()
		if err != nil || current.Language == "" {
			return lang.Default
		}
		return current.Language
	}
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

func listenUntilSignal(ctx context.Context, logger *slog.Logger, httpServer *http.Server, cfg config.Config) error {
	failed := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.Listen, "state_dir", cfg.StateDir, "version", version.String())
		failed <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-failed:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

// localSink écrit ce que le veilleur de la machine openCloud observe.
type localSink struct {
	ctx      context.Context
	services *service.Tracker
	logger   *slog.Logger
}

func (s localSink) Deliver(report service.Report) {
	if err := s.services.Record(s.ctx, machine.LocalID, report); err != nil && s.ctx.Err() == nil {
		s.logger.Error("record local services", "error", err)
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
