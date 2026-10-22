package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/auth"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/config"
	"github.com/ldesfontaine/opencloud/internal/store"
)

const authUsage = `Usage : {command} auth <status|disable> [-config chemin]

  status    dit si l'interface est protégée, par quel identifiant ou
            derrière quel en-tête d'identité du mandataire, et quelles
            identités forward_users admet
  disable   coupe l'authentification de l'interface (mot de passe oublié) :
            elle revient à sa première configuration ; serve la suit à la
            requête suivante, sans redémarrer
`

// opencloud auth : la porte de secours quand le mot de passe est perdu. Elle
// écrit dans la base sous le compte du service : qui peut la lancer peut
// déjà lire state_dir, elle n'ouvre rien de plus.
func runAuth(args []string) error {
	return authCommand(args, os.Stdout)
}

func authCommand(args []string, out io.Writer) error {
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, strings.ReplaceAll(authUsage, "{command}", brand.Command))
		return &refusal{message: "sous-commande manquante"}
	}
	command := args[0]
	if command != "status" && command != "disable" {
		fmt.Fprint(os.Stderr, strings.ReplaceAll(authUsage, "{command}", brand.Command))
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
	ctx := audit.WithActor(context.Background(), audit.CLI(currentAccount()))
	db, err := store.Open(ctx, stateDir)
	if err != nil {
		return err
	}
	defer db.Close()
	access, err := auth.New(db, logger)
	if err != nil {
		return err
	}
	access.SetAuditor(audit.New(db, logger))
	if command == "disable" {
		return disableAuth(ctx, access, out)
	}
	return printAuthStatus(ctx, access, cfg.ForwardUsers, out)
}

func disableAuth(ctx context.Context, access *auth.Service, out io.Writer) error {
	// Accès local direct à la base : l'état lu ici est celui que la
	// commande désactive.
	current, err := access.State(ctx)
	if err != nil {
		return err
	}
	err = access.Disable(ctx, current)
	if errors.Is(err, auth.ErrStateChanged) {
		return &refusal{message: "l'authentification a changé pendant la commande : relancez-la"}
	}
	if errors.Is(err, auth.ErrDisabled) {
		_, writeErr := fmt.Fprintln(out, "L'authentification de l'interface était déjà désactivée.")
		return writeErr
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, "Authentification de l'interface désactivée : l'interface revient à sa première configuration. Ouvrez-la et créez le compte : le premier qui atteint le port le crée.")
	return err
}

// printAuthStatus dit l'état de l'accès. forwardUsers est la liste de
// config.toml, qui ne se lit qu'en mode forward : dans les autres, elle ne
// sert à rien et l'afficher laisserait croire le contraire.
func printAuthStatus(ctx context.Context, access *auth.Service, forwardUsers []string, out io.Writer) error {
	state, err := access.State(ctx)
	if err != nil {
		return err
	}
	enabledAt := state.EnabledAt.UTC().Format(time.RFC3339)
	switch state.Mode {
	case auth.ModeNone:
		_, err = fmt.Fprintln(out, "mode : none — aucun compte, l'interface attend sa première configuration")
		return err
	case auth.ModeForward:
		_, err = fmt.Fprintf(out, "mode : forward — derrière un mandataire d'authentification\nen-tête d'identité : %s\nidentités admises : %s\nactivée le : %s\n",
			state.Header, describeForwardUsers(forwardUsers), enabledAt)
		return err
	}
	_, err = fmt.Fprintf(out, "mode : %s\nidentifiant : %s\nactivée le : %s\n",
		state.Mode, state.Username, enabledAt)
	return err
}

// describeForwardUsers : la liste entre guillemets, pour qu'un espace ou une
// faute de casse se voie, ou la phrase qui dit qu'il n'y a pas de liste.
func describeForwardUsers(users []string) string {
	if len(users) == 0 {
		return "toute identité transmise par le mandataire"
	}
	quoted := make([]string, len(users))
	for index, user := range users {
		quoted[index] = strconv.Quote(user)
	}
	return strings.Join(quoted, ", ")
}
