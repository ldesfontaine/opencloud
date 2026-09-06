package cli

import (
	"context"
	"io"
	"log/slog"

	"github.com/ldesfontaine/opencloud/internal/enroll"
)

// runEnrollLocal : à lancer avec sudo, jamais par l'unité — celle d'openCloud
// est fermée, et c'est pour ça que la machine openCloud passe par SSH vers
// localhost comme les autres (05-execution.md).
func runEnrollLocal(ctx context.Context, args []string, out, errOut io.Writer) error {
	configPath, err := parseConfigFlag("enroll-local", args, errOut)
	if err != nil {
		return err
	}

	cfg, err := loadConfig(configPath, slog.New(slog.NewJSONHandler(errOut, nil)))
	if err != nil {
		return err
	}
	root, err := openStateDir(cfg.StateDir)
	if err != nil {
		return err
	}
	defer root.Close()

	return enroll.EnrollLocal(ctx, enroll.SystemDeps(root, out))
}
