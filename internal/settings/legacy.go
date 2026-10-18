package settings

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/pelletier/go-toml/v2"
)

// Le fichier où vivaient les réglages avant la base, sous le répertoire
// d'état, et le temporaire qu'une écriture interrompue de l'ancien code a pu
// laisser (internal/fsx) : il porte le mot de passe du relais en clair.
const (
	legacyFileName     = "settings.toml"
	legacyTempFileName = legacyFileName + ".tmp"
)

// ErrLegacyFileKept : les réglages sont en base, mais l'ancien fichier n'a
// pas pu être effacé. Rien n'est perdu ; il sera retenté au démarrage
// suivant.
var ErrLegacyFileKept = errors.New("legacy settings file kept")

// Ce que la reprise attend de la base : écrire des réglages seulement si
// elle n'en a pas encore, et dire si elle l'a fait.
type Importer interface {
	ImportSettings(ctx context.Context, imported Settings) (bool, error)
}

// Outcome dit ce que la reprise a fait, pour le journal.
type Outcome int

const (
	// Aucun fichier : une installation neuve, ou déjà reprise.
	NoLegacyFile Outcome = iota
	// Le fichier est passé en base, puis a été effacé.
	Imported
	// La base avait déjà des réglages : elle l'emporte, le fichier est
	// effacé sans être appliqué. C'est le cas d'un démarrage interrompu
	// après la reprise, mais aussi d'un retour à un ancien binaire qui a
	// réécrit le fichier : ses changements sont perdus.
	AlreadyInDatabase
)

// ImportLegacyFile reprend settings.toml puis l'efface : les réglages n'ont
// plus qu'un endroit. Un fichier illisible arrête le démarrage, comme avant :
// rien n'est perdu, l'opérateur le corrige ou l'enlève.
func ImportLegacyFile(ctx context.Context, stateDir *os.Root, target Importer) (Outcome, error) {
	content, err := stateDir.ReadFile(legacyFileName)
	if errors.Is(err, fs.ErrNotExist) {
		return NoLegacyFile, nil
	}
	if err != nil {
		return NoLegacyFile, fmt.Errorf("read %s: %w", legacyFileName, err)
	}
	var legacy Settings
	if err := toml.NewDecoder(bytes.NewReader(content)).Decode(&legacy); err != nil {
		return NoLegacyFile, fmt.Errorf("parse %s: %w", legacyFileName, err)
	}
	imported, err := target.ImportSettings(ctx, legacy)
	if err != nil {
		return NoLegacyFile, err
	}
	outcome := AlreadyInDatabase
	if imported {
		outcome = Imported
	}
	if err := stateDir.Remove(legacyFileName); err != nil {
		return outcome, fmt.Errorf("%w: %w", ErrLegacyFileKept, err)
	}
	if err := stateDir.Remove(legacyTempFileName); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return outcome, fmt.Errorf("%w: %w", ErrLegacyFileKept, err)
	}
	return outcome, nil
}
