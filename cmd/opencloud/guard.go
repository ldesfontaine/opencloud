package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/rollback"
)

// opencloud guard : le lanceur stable de l'agent. L'unité systemd lance cette
// commande depuis une copie du binaire que root a posée hors de bin/, et elle
// lance bin/current avec le reste de la ligne (« agent -state … »). Elle rend
// à systemd le code de sortie qu'il faut pour relancer, ou pour s'arrêter.
func runGuard(args []string) error {
	flags := flag.NewFlagSet("guard", flag.ContinueOnError)
	stateDir := flags.String("state", defaultAgentStateDir, "répertoire d'état de l'agent")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() == 0 {
		catalogs, err := lang.Load()
		if err != nil {
			return err
		}
		return &refusal{message: catalogs.For(lang.Default).Get("agent.guard_usage")}
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	state, err := openStateDir(*stateDir)
	if err != nil {
		return err
	}
	defer state.Close()
	absoluteState, err := filepath.Abs(*stateDir)
	if err != nil {
		return err
	}
	bin, err := state.OpenRoot(brand.AgentBinDir)
	if err != nil {
		return err
	}
	defer bin.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	binary := filepath.Join(absoluteState, brand.AgentBinDir, brand.AgentCurrentLink)
	code := rollback.NewGuard(state, bin, binary, flags.Args(), logger).Run(ctx)
	if code == 0 {
		return nil
	}
	return &exitStatus{code: code}
}
