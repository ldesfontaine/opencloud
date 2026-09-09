package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/ldesfontaine/opencloud/internal/auth"
	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/config"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/runner"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/systemd"
	"github.com/ldesfontaine/opencloud/internal/web"
	"github.com/ldesfontaine/opencloud/migrations"
)

const (
	stateDirMode      = 0o700
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	// Une page se sert en moins de ça ; le flux en direct (SSE), plus tard,
	// repoussera sa propre échéance par http.ResponseController.
	writeTimeout    = 30 * time.Second
	idleTimeout     = 2 * time.Minute
	shutdownTimeout = 10 * time.Second

	// « Tester l'accès », en silence : assez souvent pour qu'un statut ne
	// mente pas longtemps, assez rarement pour ne pas peser sur les machines.
	probeInterval = 5 * time.Minute
)

// runServe démarre tout dans l'ordre : configuration, base, compte par défaut,
// écoute, READY=1 ; puis attend l'arrêt.
func runServe(ctx context.Context, args []string, version string, errOut io.Writer) error {
	configPath, err := parseConfigFlag("serve", args, errOut)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(errOut, nil))
	// Lu tout de suite, une seule fois (voir systemd.NewNotifier).
	notifier := systemd.NewNotifier()

	cfg, err := loadConfig(configPath, logger)
	if err != nil {
		return err
	}

	root, err := openStateDir(cfg.StateDir)
	if err != nil {
		return err
	}
	defer root.Close()

	database, err := store.Open(ctx, root, migrations.Files, logger)
	if err != nil {
		return fmt.Errorf("ouvrir la base : %w", err)
	}
	defer database.Close()

	authService := auth.New(database, logger, auth.PasswordPolicy{MinLength: cfg.MinPasswordLength})
	if err := authService.EnsureDefaultAccount(ctx, cfg.AllowDefaultPassword); err != nil {
		return fmt.Errorf("créer le compte par défaut : %w", err)
	}

	// Le runner s'arrête avant la base : une action en cours reste « running »
	// et la reprise la retrouvera au prochain démarrage.
	machines := machineAccess{root: root}
	actionRunner := runner.New(database, catalog.Service{}, machines, logger)
	defer actionRunner.Close()
	if err := actionRunner.Resume(ctx); err != nil {
		return fmt.Errorf("reprendre les actions en cours : %w", err)
	}

	// La sonde tourne tant que le service tourne ; elle s'arrête avec lui.
	probeCtx, stopProbes := context.WithCancel(ctx)
	defer stopProbes()
	checker := probe.New(database, machineProbes{access: machines}, probeInterval, logger)
	go checker.Run(probeCtx)

	// Déjà validés au chargement ; l'erreur ne peut venir que d'un Config
	// fabriqué à la main.
	trustedProxies, err := config.ParseTrustedProxies(cfg.TrustedProxies)
	if err != nil {
		return fmt.Errorf("lire les proxies de confiance : %w", err)
	}

	server, err := web.New(web.Dependencies{
		Auth:        authService,
		Machines:    database,
		Declaration: machineDeclaration{store: database},
		Enrolment:   machines,
		Enroller:    machines,
		Actions:     actionRunner,
		Catalog:     catalog.Service{},
		Prober:      machineHealth{checker: checker, store: database},

		TrustedProxies: trustedProxies,
	}, version, logger)
	if err != nil {
		return fmt.Errorf("préparer l'interface : %w", err)
	}

	listener, err := net.Listen("tcp", cfg.Listen) // bounded: config.validateListen
	if err != nil {
		return fmt.Errorf("écouter sur %s : %w", cfg.Listen, err)
	}
	return serveUntilStopped(ctx, listener, server.Handler(), notifier, logger)
}

func parseConfigFlag(command string, args []string, errOut io.Writer) (string, error) {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(errOut)
	configPath := flags.String("config", config.DefaultPath, "chemin du fichier de configuration")
	if err := flags.Parse(args); err != nil {
		return "", err
	}
	return *configPath, nil
}

func loadConfig(path string, logger *slog.Logger) (config.Config, error) {
	cfg, warnings, err := config.Load(path)
	if err != nil {
		return config.Config{}, err
	}
	for _, warning := range warnings {
		if warning.Remedy == "" {
			logger.Warn("unknown config key ignored", "key", warning.Key, "config", path)
			continue
		}
		logger.Warn("retired config key ignored", "key", warning.Key, "remedy", warning.Remedy, "config", path)
	}
	return cfg, nil
}

// openStateDir crée le répertoire d'état s'il manque — sous systemd c'est
// StateDirectory= qui le fait, à la main c'est nous — puis l'ouvre une fois.
func openStateDir(dir string) (*os.Root, error) {
	if err := os.MkdirAll(dir, stateDirMode); err != nil { // bounded: config.validateStateDir
		return nil, fmt.Errorf("créer le répertoire d'état %s : %w", dir, err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("ouvrir le répertoire d'état %s : %w", dir, err)
	}
	return root, nil
}

func serveUntilStopped(ctx context.Context, listener net.Listener, handler http.Handler, notifier *systemd.Notifier, logger *slog.Logger) error {
	httpServer := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	served := make(chan error, 1)
	go func() {
		served <- httpServer.Serve(listener)
	}()

	logger.Info("listening", "address", listener.Addr().String())
	if err := notifier.Ready(); err != nil {
		logger.Warn("systemd not notified", "error", err)
	}

	select {
	case err := <-served:
		return fmt.Errorf("servir : %w", err)
	case <-ctx.Done():
	}

	logger.Info("stopping")
	if err := notifier.Stopping(); err != nil {
		logger.Warn("systemd not notified", "error", err)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("arrêter : %w", err)
	}
	return nil
}
