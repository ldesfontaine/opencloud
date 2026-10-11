package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/ldesfontaine/opencloud/internal/auth"
	"github.com/ldesfontaine/opencloud/internal/config"
	"github.com/ldesfontaine/opencloud/internal/store"
)

const authUsage = `Usage : opencloud auth <status|disable> [-config chemin]

  status    dit si l'interface est protégée, par quel identifiant ou
            derrière quel en-tête d'identité du mandataire
  disable   coupe l'authentification de l'interface (mot de passe oublié) ;
            serve la suit à la requête suivante, sans redémarrer
`

// opencloud auth : la porte de secours quand le mot de passe est perdu. Elle
// écrit dans la base sous le compte du service : qui peut la lancer peut
// déjà lire state_dir, elle n'ouvre rien de plus.
func runAuth(args []string) error {
	return authCommand(args, os.Stdout)
}

func authCommand(args []string, out io.Writer) error {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, authUsage)
		return &refusal{message: "sous-commande manquante"}
	}
	command := args[0]
	if command != "status" && command != "disable" {
		fmt.Fprint(os.Stderr, authUsage)
		return &refusal{message: "sous-commande inconnue : " + command}
	}
	flags := flag.NewFlagSet("auth "+command, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", config.DefaultPath, "chemin du fichier de configuration")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	cfg, _, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	stateDir, err := openStateDir(cfg.StateDir)
	if err != nil {
		return err
	}
	defer stateDir.Close()
	ctx := context.Background()
	db, err := store.Open(ctx, stateDir)
	if err != nil {
		return err
	}
	defer db.Close()
	access, err := auth.New(db, logger)
	if err != nil {
		return err
	}
	if command == "disable" {
		return disableAuth(ctx, access, out)
	}
	return printAuthStatus(ctx, access, out)
}

func disableAuth(ctx context.Context, access *auth.Service, out io.Writer) error {
	err := access.Disable(ctx)
	if errors.Is(err, auth.ErrDisabled) {
		_, writeErr := fmt.Fprintln(out, "L'authentification de l'interface était déjà désactivée.")
		return writeErr
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "Authentification de l'interface désactivée : l'interface est ouverte à qui atteint le port. Réactivez-la sous Paramètres.")
	return err
}

func printAuthStatus(ctx context.Context, access *auth.Service, out io.Writer) error {
	state, err := access.State(ctx)
	if err != nil {
		return err
	}
	enabledAt := state.EnabledAt.UTC().Format(time.RFC3339)
	switch state.Mode {
	case auth.ModeNone:
		_, err = fmt.Fprintln(out, "mode : none — interface ouverte, sans authentification")
		return err
	case auth.ModeForward:
		_, err = fmt.Fprintf(out, "mode : forward — derrière un mandataire d'authentification\nen-tête d'identité : %s\nactivée le : %s\n",
			state.Header, enabledAt)
		return err
	}
	_, err = fmt.Fprintf(out, "mode : %s\nidentifiant : %s\nactivée le : %s\n",
		state.Mode, state.Username, enabledAt)
	return err
}
