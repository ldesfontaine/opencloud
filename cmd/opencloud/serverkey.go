package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/brand"
)

// openServerKey lit la clé qui signe les ordres, ou la crée au premier
// démarrage. Une clé illisible arrête serve : en tirer une neuve ferait
// refuser tous les ordres par les agents, qui ont épinglé l'ancienne.
func openServerKey(stateDir *os.Root, logger *slog.Logger) (*action.Key, error) {
	key, created, err := action.LoadOrCreateKey(stateDir)
	keyPath := filepath.Join(stateDir.Name(), brand.ServerKeyFile)
	switch {
	case errors.Is(err, action.ErrKeyFileMode):
		return nil, &refusal{message: fmt.Sprintf("%s est lisible par d'autres comptes : réservez-le à son propriétaire (chmod 600 %s)", brand.ServerKeyFile, keyPath)}
	case errors.Is(err, action.ErrKeyFileInvalid):
		return nil, &refusal{message: fmt.Sprintf("%s n'est pas une clé intacte (%v) : restaurez la clé sauvegardée avec la base ; sans elle, chaque agent est à ré-enrôler (voir installation.md)", brand.ServerKeyFile, err)}
	case err != nil:
		return nil, fmt.Errorf("open server key: %w", err)
	}
	if created {
		logger.Info("server key created", "file", brand.ServerKeyFile, "fingerprint", key.Fingerprint())
	}
	return key, nil
}
