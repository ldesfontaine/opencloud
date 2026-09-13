package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ldesfontaine/opencloud/internal/agent"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/version"
)

const defaultAgentStateDir = "/var/lib/opencloud/agent"

// Un refus se dit à l'opérateur dans sa langue et sort avec le code 2 ; une
// erreur porte une phrase puis le détail, code 1.
type refusal struct {
	message string
}

func (r *refusal) Error() string {
	return r.message
}

// opencloud agent : le premier lancement porte -server et -token et
// s'enrôle ; les suivants relisent l'identité et se connectent.
func runAgent(args []string) error {
	flags := flag.NewFlagSet("agent", flag.ContinueOnError)
	server := flags.String("server", "", "adresse d'openCloud, premier lancement seulement")
	token := flags.String("token", "", "jeton d'enrôlement, premier lancement seulement")
	pin := flags.String("pin", "", "empreinte SHA-256 du certificat d'openCloud, s'il est auto-signé")
	language := flags.String("lang", string(lang.Default), "langue des messages, premier lancement seulement")
	stateDir := flags.String("state", defaultAgentStateDir, "répertoire d'état de l'agent")
	if err := flags.Parse(args); err != nil {
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("agent starting", "version", version.String(), "state_dir", *stateDir)
	err = agent.Run(ctx, agent.Options{
		StateDir: root,
		Server:   *server,
		Token:    *token,
		Pin:      *pin,
		Language: code,
		Version:  version.Number(),
		Logger:   logger,
	})
	if err == nil {
		return nil
	}
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
