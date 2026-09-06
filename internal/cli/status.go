package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/ldesfontaine/opencloud/internal/config"
	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/store"
)

const probeTimeout = 3 * time.Second

// ErrServiceDown : l'interface ne répond pas à la sonde.
var ErrServiceDown = errors.New("le service ne répond pas")

// runStatus dit ce qu'il voit, ligne par ligne, et rend une erreur si le
// service ne répond pas — pour un script comme pour un œil.
func runStatus(ctx context.Context, args []string, out, errOut io.Writer) error {
	configPath, err := parseConfigFlag("status", args, errOut)
	if err != nil {
		return err
	}

	cfg, warnings, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(out, "configuration : %v\n", err)
		return err
	}
	fmt.Fprintf(out, "configuration : %s (valide, %d avertissement(s))\n", configPath, len(warnings))
	for _, warning := range warnings {
		fmt.Fprintf(out, "  %s\n", warning)
	}
	fmt.Fprintf(out, "écoute        : %s\n", cfg.Listen)
	fmt.Fprintf(out, "état          : %s (%s)\n", cfg.StateDir, describeStateDir(cfg.StateDir))

	// Un verrou de migration se dit avant tout le reste : le service ne
	// démarrera pas tant qu'il est là.
	if err := store.CheckMigrationLock(cfg.StateDir); err != nil {
		var refused refusal.Refusal
		if errors.As(err, &refused) {
			fmt.Fprintf(out, "migration     : %s\n", refused.Cause)
			fmt.Fprintf(out, "                → %s\n", refused.Remedy)
		}
		return err
	}

	version, err := probeService(ctx, cfg.Listen)
	if err != nil {
		fmt.Fprintf(out, "service       : ne répond pas (%v)\n", err)
		return ErrServiceDown
	}
	fmt.Fprintf(out, "service       : répond, version %s\n", version)
	return nil
}

func describeStateDir(dir string) string {
	info, err := os.Stat(filepath.Join(dir, store.DatabaseFileName)) // bounded: config.validateStateDir
	if errors.Is(err, os.ErrNotExist) {
		return "base absente, créée au premier démarrage"
	}
	if err != nil {
		return fmt.Sprintf("illisible : %v", err)
	}
	return fmt.Sprintf("base présente, %d Ko", info.Size()/1024)
}

// probeService interroge /healthz. Une écoute sur toutes les adresses se
// sonde en local.
func probeService(ctx context.Context, listen string) (string, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}

	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(probeCtx, http.MethodGet,
		"http://"+net.JoinHostPort(host, port)+"/healthz", nil)
	if err != nil {
		return "", err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("code %d", response.StatusCode)
	}

	var health struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(response.Body).Decode(&health); err != nil {
		return "", fmt.Errorf("réponse illisible : %w", err)
	}
	return health.Version, nil
}
