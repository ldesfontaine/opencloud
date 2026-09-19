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

	"github.com/ldesfontaine/opencloud/internal/config"
	"github.com/ldesfontaine/opencloud/internal/dockerapi"
	"github.com/ldesfontaine/opencloud/internal/dockerwatch"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/hostinfo"
	"github.com/ldesfontaine/opencloud/internal/live"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/resource"
	"github.com/ldesfontaine/opencloud/internal/sampler"
	"github.com/ldesfontaine/opencloud/internal/server"
	"github.com/ldesfontaine/opencloud/internal/service"
	"github.com/ldesfontaine/opencloud/internal/settings"
	"github.com/ldesfontaine/opencloud/internal/store"
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
	// Cinq boucles de fond : les échéances, le rollup et la purge, la
	// mesure de cette machine, qui est son propre agent, la purge des
	// services et le veilleur Docker. Elles finissent avant que la base
	// ne se ferme.
	var loops sync.WaitGroup
	runLoop(&loops, func() { heartbeats.Watch(ctx) })
	runLoop(&loops, func() { resources.Watch(ctx) })
	runLoop(&loops, func() { resources.SampleLocal(ctx, machine.LocalID, sampler.New(), resource.SampleInterval) })
	runLoop(&loops, func() { services.Watch(ctx) })
	runLoop(&loops, func() { watcher.Run(ctx) })
	defer func() { stop(); loops.Wait() }()

	server, err := server.New(server.Options{
		Logger:         logger,
		Version:        version.Number(),
		Settings:       settings.New(stateDir),
		Machines:       machines,
		Heartbeats:     heartbeats,
		Resources:      resources,
		Services:       services,
		Live:           bus,
		PublicURL:      cfg.PublicURL,
		TrustedProxies: cfg.TrustedPrefixes(),
	})
	if err != nil {
		return err
	}
	// Le serveur tient les flux des agents : c'est lui qui leur commande.
	services.SetCommander(server)
	httpServer := &http.Server{
		Addr:              cfg.Listen,
		Handler:           server,
		ReadHeaderTimeout: readHeaderTimeout,
	}
	return listenUntilSignal(ctx, logger, httpServer, cfg)
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
