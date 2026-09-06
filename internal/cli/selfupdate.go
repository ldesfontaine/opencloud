package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/ldesfontaine/opencloud/internal/config"
	"github.com/ldesfontaine/opencloud/internal/selfupdate"
)

// Après le redémarrage, le temps laissé au service pour répondre en nouvelle
// version : sauvegarde et migrations comprises. systemctl a déjà attendu
// READY=1 ; on ne déclare pas l'échec avant systemd, d'où le budget de
// TimeoutStartSec de l'unité, repris tel quel.
const serviceStartTimeout = selfupdate.UnitStartTimeout

// runSelfUpdate : à lancer avec sudo, jamais par l'unité. Il lit la
// configuration pour le jeton GitHub et l'adresse d'écoute, remplace le
// binaire qui l'exécute, puis attend que le service réponde.
func runSelfUpdate(ctx context.Context, args []string, version string, out, errOut io.Writer) error {
	flags := flag.NewFlagSet("self-update", flag.ContinueOnError)
	flags.SetOutput(errOut)
	configPath := flags.String("config", config.DefaultPath, "chemin du fichier de configuration")
	requested := flags.String("version", "", "version à installer (vX.Y.Z) ; par défaut, la dernière release")
	checkOnly := flags.Bool("check", false, "dire ce qui est disponible sans rien modifier")
	if err := flags.Parse(args); err != nil {
		return err
	}

	cfg, err := loadConfig(*configPath, slog.New(slog.NewJSONHandler(errOut, nil)))
	if err != nil {
		return err
	}
	executable, err := currentExecutable()
	if err != nil {
		return err
	}

	client := selfupdate.NewClient(cfg.GitHubToken, "opencloud/"+version)
	result, err := selfupdate.New(client, out).Run(ctx, selfupdate.Options{
		CurrentVersion:   version,
		ExecutablePath:   executable,
		RequestedVersion: *requested,
		CheckOnly:        *checkOnly,
	})
	if err != nil {
		return err
	}
	if !result.Updated || !result.Restarted {
		return nil
	}
	return waitForServiceVersion(ctx, cfg.Listen, result.Version.String(), out)
}

// currentExecutable résout les liens : /usr/bin/opencloud pointe sur
// /opt/opencloud/bin/opencloud, et c'est ce dernier qu'on remplace.
func currentExecutable() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("situer le binaire : %w", err)
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return "", fmt.Errorf("résoudre %s : %w", executable, err)
	}
	return resolved, nil
}

func waitForServiceVersion(ctx context.Context, listen, expected string, out io.Writer) error {
	deadline := time.Now().Add(serviceStartTimeout)
	for {
		version, err := probeService(ctx, listen)
		if err == nil && version == expected {
			fmt.Fprintf(out, "service           : répond, version %s\n", version)
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("le service ne répond pas en version %s après %s : voir journalctl -u opencloud", expected, serviceStartTimeout)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
