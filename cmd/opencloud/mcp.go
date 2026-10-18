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
	"github.com/ldesfontaine/opencloud/internal/auth"
	"github.com/ldesfontaine/opencloud/internal/config"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/lifecycle"
	"github.com/ldesfontaine/opencloud/internal/live"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/mail"
	"github.com/ldesfontaine/opencloud/internal/mcp"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/resource"
	"github.com/ldesfontaine/opencloud/internal/server"
	"github.com/ldesfontaine/opencloud/internal/service"
	"github.com/ldesfontaine/opencloud/internal/status"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/update"
	"github.com/ldesfontaine/opencloud/internal/version"
)

// opencloud mcp : un client IA local (Claude Code, Claude Desktop) lance
// ce processus et lui parle en JSON-RPC sur stdin/stdout. Il lit la même
// base que serve, sans socket ni identifiants, et n'écrit rien d'autre que
// le chiffrement des secrets restés en clair par une montée de version : il
// ne tient ni le bus du direct ni les flux des agents. Il lit la clé de
// serve, sans jamais la créer. Les journaux vont sur stderr, stdout est le
// canal du protocole.
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
	if err := openSecrets(ctx, stateDir, db, logger, false); err != nil {
		return err
	}

	// Les mêmes composants que serve, sans aucune boucle ni écouteur : ils
	// ne servent qu'à lire. Le bus n'a pas d'abonné, les flux pas d'agent.
	machines := machine.New(db, machine.NewSessions(), logger)
	// Les flux des agents vivent dans serve : ici, une machine est en ligne
	// si son dernier signal est récent.
	machines.SetPresence(machine.RecentSignal{})
	heartbeats := heartbeat.New(db, logger)
	resources := resource.New(db, logger)
	services := service.New(db, logger)
	probes := probe.New(db, logger)
	declared := directory.New(db, logger)
	updates := update.New(db, logger)
	// Ce processus ne fait que lire : l'écart voit les conteneurs que serve
	// a rattachés, il n'écrit et ne rattache rien.
	declared.SetObserved(services, updates)
	statusPage := status.New(db, machines, db, logger)
	catalogs, err := lang.Load()
	if err != nil {
		return fmt.Errorf("load languages: %w", err)
	}
	alerts := alert.New(db, logger)
	notifier := alert.NewNotifier(db, catalogs, languageOf(db), logger)
	notifier.SetPublicURL(cfg.PublicURL)
	notifier.SetMail(mailRelayOf(db), mail.NewSender(alert.MailTimeout))
	access, err := auth.New(db, logger)
	if err != nil {
		return err
	}
	// La fin de support : ce que serve a gardé de sa dernière lecture. Ce
	// processus ne lit jamais la source lui-même.
	support := lifecycle.New(services.Images, machines.Systems, lifecycle.NewFileCache(stateDir), lifecycle.NewHTTPSource(), lifecycleEnabledOf(db, logger), logger)
	if err := support.Load(); err != nil {
		logger.Warn("load lifecycle snapshot", "error", err)
	}
	srv, err := server.New(server.Options{
		Logger:         logger,
		Version:        version.Number(),
		Settings:       db,
		Machines:       machines,
		Heartbeats:     heartbeats,
		Resources:      resources,
		Services:       services,
		Probes:         probes,
		Directory:      declared,
		Updates:        updates,
		Lifecycle:      support,
		Status:         statusPage,
		Alerts:         alerts,
		Notifier:       notifier,
		MCP:            mcp.New(db, logger),
		Auth:           access,
		Live:           live.New(),
		PublicLive:     live.New(),
		PublicURL:      cfg.PublicURL,
		Listen:         cfg.Listen,
		TrustedProxies: cfg.TrustedPrefixes(),
		ConfigPath:     *configPath,
	})
	if err != nil {
		return err
	}
	logger.Info("mcp stdio session", "state_dir", cfg.StateDir, "version", version.String())
	return srv.RunMCPStdio(ctx)
}
