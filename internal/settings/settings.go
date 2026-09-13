package settings

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/pelletier/go-toml/v2"

	"github.com/ldesfontaine/opencloud/internal/fsx"
	"github.com/ldesfontaine/opencloud/internal/lang"
)

const (
	fileName = "settings.toml"
	fileMode = 0o600
)

type Settings struct {
	// Langue de l'interface ; vide tant que l'opérateur n'a rien choisi.
	Language lang.Code `toml:"language"`
}

// Store lit et écrit settings.toml sous le répertoire d'état.
type Store struct {
	root *os.Root
}

func New(root *os.Root) *Store {
	return &Store{root: root}
}

// Load renvoie les réglages du fichier, ou des réglages vides s'il n'existe
// pas encore : la première installation n'a rien à lire.
func (s *Store) Load() (Settings, error) {
	content, err := s.root.ReadFile(fileName)
	if errors.Is(err, fs.ErrNotExist) {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("read settings: %w", err)
	}
	var settings Settings
	if err := toml.NewDecoder(bytes.NewReader(content)).Decode(&settings); err != nil {
		return Settings{}, fmt.Errorf("parse settings: %w", err)
	}
	return settings, nil
}

func (s *Store) Save(settings Settings) error {
	content, err := toml.Marshal(settings)
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	if err := fsx.WriteAtomic(s.root, fileName, content, fileMode); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}
