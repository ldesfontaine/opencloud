package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ldesfontaine/opencloud/internal/config"
	"github.com/ldesfontaine/opencloud/internal/live"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/server"
	"github.com/ldesfontaine/opencloud/internal/store"
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

	// Les mêmes composants que serve, par le même câblage. Ni direct, ni
	// boucle, ni commandeur, ni machine openCloud : ce processus ne fait que
	// lire, le stdio n'expose aucun outil qui écrit, et serve seul tient les
	// flux des agents.
	parts, err := buildComponents(cfg, stateDir, db, logger)
	if err != nil {
		return err
	}
	// Les flux des agents vivent dans serve : ici, une machine est en ligne
	// si son dernier signal est récent.
	parts.machines.SetPresence(machine.RecentSignal{})
	// La fin de support : ce que serve a gardé de sa dernière lecture. Ce
	// processus ne lit jamais la source lui-même.
	if err := parts.support.Load(); err != nil {
		logger.Warn("load lifecycle snapshot", "error", err)
	}
	// Deux bus que personne n'écoute : le direct n'a pas d'onglet ici.
	options := parts.serverOptions(cfg, *configPath)
	options.Live = live.New()
	options.PublicLive = live.New()
	srv, err := server.New(options)
	if err != nil {
		return err
	}
	logger.Info("mcp stdio session", "state_dir", cfg.StateDir, "version", version.String())
	return srv.RunMCPStdio(ctx)
}
