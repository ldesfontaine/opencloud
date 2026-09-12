package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ldesfontaine/opencloud/internal/config"
	"github.com/ldesfontaine/opencloud/internal/version"
	"github.com/ldesfontaine/opencloud/web"
)

const (
	readHeaderTimeout = 10 * time.Second
	shutdownTimeout   = 10 * time.Second
	stateDirMode      = 0o700
)

func runServe(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	configPath := flags.String("config", config.DefaultPath, "chemin du fichier de configuration")
	if err := flags.Parse(args); err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	cfg, warnings, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	for _, warning := range warnings {
		logger.Warn("config", "detail", warning)
	}
	stateDir, err := openStateDir(cfg.StateDir)
	if err != nil {
		return err
	}
	defer stateDir.Close()

	server, err := web.New(web.Options{Logger: logger, Version: version.Number()})
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Addr:              cfg.Listen,
		Handler:           server,
		ReadHeaderTimeout: readHeaderTimeout,
	}
	return listenUntilSignal(logger, httpServer, cfg)
}

// Le répertoire d'état est ouvert une fois par os.Root ; les composants qui
// viendront y écrire passeront par lui.
func openStateDir(path string) (*os.Root, error) {
	if err := os.MkdirAll(path, stateDirMode); err != nil { // bounded: config.StateDir
		return nil, fmt.Errorf("create state dir: %w", err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, fmt.Errorf("open state dir: %w", err)
	}
	return root, nil
}

func listenUntilSignal(logger *slog.Logger, httpServer *http.Server, cfg config.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	failed := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.Listen, "state_dir", cfg.StateDir, "version", version.String())
		failed <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-failed:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}
