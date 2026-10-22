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
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/release"
	"github.com/ldesfontaine/opencloud/internal/trust"
	"github.com/ldesfontaine/opencloud/internal/version"
)

const defaultAgentStateDir = brand.AgentStateDir

// updatedExitCode : « mis à jour, relance-moi ». Ni 0 ni 1 (une erreur) ni 2
// (un refus, que systemd ne relance pas).
const updatedExitCode = 3

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
	// -token n'existe que pour être refusé avec une phrase claire, plutôt que
	// l'erreur générique d'un drapeau inconnu.
	flags.String("token", "", "refusé : le jeton se lit sur l'entrée standard")
	pin := flags.String("pin", "", "empreinte SHA-256 du certificat d'"+brand.ProductName+", s'il est auto-signé")
	language := flags.String("lang", string(lang.Default), "langue des messages, premier lancement seulement")
	rights := flags.String("rights", "", "droits cochés à l'installation, premier lancement seulement (observe par défaut)")
	rootDir := flags.String("root-dir", brand.WorkspaceRoot, "racine des services déployés, premier lancement seulement")
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
	// Dans la ligne de commande, le jeton serait lisible de tous les comptes
	// de la machine par /proc : le premier qui le copie prend l'identité.
	if flagIsSet(flags, "token") {
		return &refusal{message: catalogs.For(code).Get("agent.token_flag_refused")}
	}
	// Les droits se jugent avant l'enrôlement : un droit mal écrit ne doit
	// pas consommer le jeton.
	capabilities, err := machine.ParseCapabilities(*rights)
	if err != nil {
		return &refusal{message: catalogs.For(code).Get("agent.rights_invalid")}
	}
	// La racine aussi se juge avant l'enrôlement, pour la même raison.
	if !machine.ValidCapabilityPath(*rootDir) {
		return &refusal{message: catalogs.For(code).Get("agent.root_invalid")}
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	root, err := openStateDir(*stateDir)
	if err != nil {
		return err
	}
	defer root.Close()

	// Un état mal gardé ne coupe pas l'agent : il ne garde que observe, et
	// l'écart se lit sur la fiche de la machine. Jugé après l'ouverture : le
	// dossier ouvert doit être celui que le chemin désigne.
	unsafeState := agent.CheckStateDir(root, *stateDir)

	enrollmentToken, err := enrollmentTokenFor(root, *server, os.Stdin, os.Stderr, stdinIsTerminal(), catalogs.For(code))
	if err != nil {
		return explainAgentError(err, root, code, catalogs)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("agent starting", "version", version.String(), "state_dir", *stateDir)
	// Sans clé de version embarquée, l'agent refuse agent.update : il ne
	// saurait rien vérifier.
	releaseKey, keyErr := release.TrustedKey()
	err = agent.Run(ctx, agent.Options{
		StateDir:     root,
		UnsafeState:  unsafeState,
		Server:       *server,
		Token:        enrollmentToken,
		Pin:          *pin,
		AllowPlain:   *allowPlain,
		Language:     code,
		Rights:       capabilities,
		RootDir:      *rootDir,
		Version:      version.Number(),
		DockerSocket: *dockerSocket,
		Roots:        roots,
		Logger:       logger,

		ReleaseKey:    releaseKey,
		HasReleaseKey: keyErr == nil,
	})
	if err == nil {
		return nil
	}
	// Mis à jour : le lien current vise la nouvelle version, systemd relance.
	if errors.Is(err, agent.ErrUpdated) {
		logger.Info("agent updated, exiting for systemd to restart it")
		return &exitStatus{code: updatedExitCode}
	}
	return explainAgentError(err, root, code, catalogs)
}

func flagIsSet(flags *flag.FlagSet, name string) bool {
	set := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

// enrollmentTokenFor rend le jeton à présenter, lu sur l'entrée standard, ou
// "" quand l'agent n'a pas à s'enrôler. L'entrée n'est lue que pour un agent
// sans identité et avec -server : un agent déjà enrôlé, lancé par systemd
// (entrée sur /dev/null), ne doit jamais s'y bloquer.
func enrollmentTokenFor(stateDir *os.Root, server string, in io.Reader, prompt io.Writer, interactive bool, text lang.Catalog) (string, error) {
	if _, err := agent.LoadIdentity(stateDir); !errors.Is(err, agent.ErrNotEnrolled) || server == "" {
		return "", nil
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
