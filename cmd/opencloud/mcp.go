package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/config"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/live"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/mcp"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/resource"
	"github.com/ldesfontaine/opencloud/internal/server"
	"github.com/ldesfontaine/opencloud/internal/service"
	"github.com/ldesfontaine/opencloud/internal/settings"
	"github.com/ldesfontaine/opencloud/internal/status"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/update"
	"github.com/ldesfontaine/opencloud/internal/version"
)

// opencloud mcp : un client IA local (Claude Code, Claude Desktop) lance
// ce processus et lui parle en JSON-RPC sur stdin/stdout. Il lit la même
// base que serve, sans socket ni identifiants, et n'écrit rien : il ne
// tient ni le bus du direct ni les flux des agents. Les journaux vont sur
// stderr, stdout est le canal du protocole.
func runMCP(args []string) error {
	flags := flag.NewFlagSet("mcp", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
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

	// Les mêmes composants que serve, sans aucune boucle ni écouteur : ils
	// ne servent qu'à lire. Le bus n'a pas d'abonné, les flux pas d'agent.
	machines := machine.New(db, machine.NewSessions(), logger)
	heartbeats := heartbeat.New(db, logger)
	resources := resource.New(db, logger)
	services := service.New(db, logger)
	probes := probe.New(db, logger)
	updates := update.New(db, logger)
	preferences := settings.New(stateDir)
	statusPage := status.New(db, machines, preferences, logger)
	catalogs, err := lang.Load()
	if err != nil {
		return fmt.Errorf("load languages: %w", err)
	}
	alerts := alert.New(db, logger)
	notifier := alert.NewNotifier(db, catalogs, languageOf(preferences), logger)
	srv, err := server.New(server.Options{
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
		MCP:            mcp.New(db, logger),
		Live:           live.New(),
		PublicLive:     live.New(),
		PublicURL:      cfg.PublicURL,
		TrustedProxies: cfg.TrustedPrefixes(),
		ConfigPath:     *configPath,
	})
	if err != nil {
		return err
	}
	logger.Info("mcp stdio session", "state_dir", cfg.StateDir, "version", version.String())
	return srv.RunMCPStdio(ctx)
}
