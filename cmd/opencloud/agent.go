package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ldesfontaine/opencloud/internal/agent"
	"github.com/ldesfontaine/opencloud/internal/version"
)

const defaultAgentStateDir = "/var/lib/opencloud/agent"

// opencloud agent : le premier lancement porte -server et -token et
// s'enrôle ; les suivants relisent l'identité et se connectent.
func runAgent(args []string) error {
	flags := flag.NewFlagSet("agent", flag.ContinueOnError)
	server := flags.String("server", "", "adresse d'openCloud, premier lancement seulement")
	token := flags.String("token", "", "jeton d'enrôlement, premier lancement seulement")
	pin := flags.String("pin", "", "empreinte SHA-256 du certificat d'openCloud, s'il est auto-signé")
	stateDir := flags.String("state", defaultAgentStateDir, "répertoire d'état de l'agent")
	if err := flags.Parse(args); err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	root, err := openStateDir(*stateDir)
	if err != nil {
		return err
	}
	defer root.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("agent starting", "version", version.String(), "state_dir", *stateDir)
	if err := agent.Run(ctx, agent.Options{
		StateDir: root,
		Server:   *server,
		Token:    *token,
		Pin:      *pin,
		Version:  version.Number(),
		Logger:   logger,
	}); err != nil {
		return fmt.Errorf("agent: %w", err)
	}
	return nil
}
