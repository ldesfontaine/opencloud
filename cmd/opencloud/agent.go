package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/ldesfontaine/opencloud/internal/agent"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/dockerapi"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/trust"
	"github.com/ldesfontaine/opencloud/internal/version"
)

const defaultAgentStateDir = brand.AgentStateDir

// Un refus se dit à l'opérateur dans sa langue et sort avec le code 2 ; une
// erreur porte une phrase puis le détail, code 1.
type refusal struct {
	message string
}

func (r *refusal) Error() string {
	return r.message
}

// opencloud agent : le premier lancement porte -server, reçoit le jeton sur
// l'entrée standard et s'enrôle ; les suivants relisent l'identité et se
// connectent.
func runAgent(args []string) error {
	flags := flag.NewFlagSet("agent", flag.ContinueOnError)
	server := flags.String("server", "", "adresse d'"+brand.ProductName+", premier lancement seulement")
	token := flags.String("token", "", "déprécié, premier lancement seulement : visible de tous les comptes de la machine via ps ; envoyez le jeton sur l'entrée standard")
	pin := flags.String("pin", "", "empreinte SHA-256 du certificat d'"+brand.ProductName+", s'il est auto-signé")
	language := flags.String("lang", string(lang.Default), "langue des messages, premier lancement seulement")
	allowPlain := flags.Bool("allow-plain", false, "accepte http:// vers un serveur distant (réseau déjà chiffré seulement)")
	stateDir := flags.String("state", defaultAgentStateDir, "répertoire d'état de l'agent")
	dockerSocket := flags.String("docker-socket", dockerapi.DefaultSocket, "socket du démon Docker de cette machine")
	caFile := flags.String("ca-file", "", "paquet PEM d'une autorité interne, ajouté au magasin du système pour les sondes")
	if err := flags.Parse(args); err != nil {
		return err
	}
	// Les autorités avant tout le reste : un chemin faux arrête l'agent
	// plutôt que de lui faire refuser toutes les chaînes en silence.
	roots, err := trust.Load(*caFile)
	if err != nil {
		return err
	}
	code, ok := lang.Parse(*language)
	if !ok {
		return fmt.Errorf("unknown language %q", *language)
	}
	catalogs, err := lang.Load()
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	root, err := openStateDir(*stateDir)
	if err != nil {
		return err
	}
	defer root.Close()

	enrollmentToken, err := enrollmentTokenFor(root, *server, *token, os.Stdin, os.Stderr, stdinIsTerminal(), catalogs.For(code), logger)
	if err != nil {
		return explainAgentError(err, root, code, catalogs)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("agent starting", "version", version.String(), "state_dir", *stateDir)
	err = agent.Run(ctx, agent.Options{
		StateDir:     root,
		Server:       *server,
		Token:        enrollmentToken,
		Pin:          *pin,
		AllowPlain:   *allowPlain,
		Language:     code,
		Version:      version.Number(),
		DockerSocket: *dockerSocket,
		Roots:        roots,
		Logger:       logger,
	})
	if err == nil {
		return nil
	}
	return explainAgentError(err, root, code, catalogs)
}

// enrollmentTokenFor rend le jeton à présenter, ou "" quand l'agent n'a pas à
// s'enrôler. Il se lit sur l'entrée standard : dans la ligne de commande, il
// serait lisible de tous les comptes de la machine par /proc, et le premier
// qui le copie prend l'identité de la machine. L'entrée n'est lue que pour un
// agent sans identité et avec -server : un agent déjà enrôlé, lancé par
// systemd (entrée sur /dev/null), ne doit jamais s'y bloquer.
func enrollmentTokenFor(stateDir *os.Root, server, flagToken string, in io.Reader, prompt io.Writer, interactive bool, text lang.Catalog, logger *slog.Logger) (string, error) {
	if _, err := agent.LoadIdentity(stateDir); !errors.Is(err, agent.ErrNotEnrolled) || server == "" {
		return flagToken, nil
	}
	if flagToken != "" {
		logger.Warn("enrollment token on the command line", "message", text.Get("agent.token_flag_deprecated"))
		return flagToken, nil
	}
	if interactive {
		fmt.Fprint(prompt, text.Get("agent.token_prompt"))
	}
	return readEnrollmentToken(in)
}

// maxTokenLine borne la lecture : un jeton tient en quelques dizaines
// d'octets, un flux sans fin n'a pas à remplir la mémoire.
const maxTokenLine = 4096

// readEnrollmentToken lit une ligne et en retire les espaces. Une entrée
// fermée ou vide n'est pas un jeton.
func readEnrollmentToken(in io.Reader) (string, error) {
	line, err := bufio.NewReader(io.LimitReader(in, maxTokenLine)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read enrollment token: %w", err)
	}
	token := strings.TrimSpace(line)
	if token == "" {
		return "", agent.ErrTokenMissing
	}
	return token, nil
}

// explainAgentError dit l'échec à l'opérateur dans sa langue, un refus
// (code 2) ou une erreur (code 1).
func explainAgentError(err error, root *os.Root, code lang.Code, catalogs lang.Catalogs) error {
	// L'identité connaît la langue choisie à l'installation ; le drapeau ne
	// sert qu'au premier lancement.
	if identity, loadErr := agent.LoadIdentity(root); loadErr == nil && identity.Language != "" {
		code = identity.Language
	}
	message, refused := agent.Explain(err, catalogs.For(code))
	if refused {
		return &refusal{message: message}
	}
	return errors.New(message)
}
