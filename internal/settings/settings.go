package settings

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"

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
	// La page de statut publique : son titre, son annonce en texte brut,
	// sa langue. Vides : pas de titre, pas d'annonce, la langue de
	// l'interface.
	StatusTitle        string    `toml:"status_title"`
	StatusAnnouncement string    `toml:"status_announcement"`
	StatusLanguage     lang.Code `toml:"status_language"`
	// Les seuils d'alerte du disque, en pourcentage ; zéro tant que
	// l'opérateur n'a rien réglé, et le moteur prend alors les siens.
	DiskAttentionPercent int `toml:"disk_attention_percent"`
	DiskDangerPercent    int `toml:"disk_danger_percent"`
}

// Store est le seul propriétaire de settings.toml : chaque écrivain y tient
// ses propres clés, et passe par Update pour ne pas écraser celles des autres.
// Un seul Store par répertoire d'état et par processus.
type Store struct {
	root *os.Root
	// Sérialise relire-modifier-écrire : sans lui, deux écrivains relisent
	// le même fichier et le dernier efface la clé du premier.
	mu sync.Mutex
}

func New(root *os.Root) *Store {
	return &Store{root: root}
}

// Load renvoie les réglages du fichier, ou des réglages vides s'il n'existe
// pas encore : la première installation n'a rien à lire.
func (s *Store) Load() (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// Update relit le fichier, applique change et réécrit, sans qu'un autre
// écrivain puisse se glisser entre la lecture et l'écriture. change ne
// touche que ses propres clés. Renvoie les réglages écrits.
func (s *Store) Update(change func(*Settings)) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.load()
	if err != nil {
		return Settings{}, err
	}
	change(&current)
	if err := s.save(current); err != nil {
		return Settings{}, err
	}
	return current, nil
}

func (s *Store) load() (Settings, error) {
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

func (s *Store) save(settings Settings) error {
	content, err := toml.Marshal(settings)
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	if err := fsx.WriteAtomic(s.root, fileName, content, fileMode); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}
