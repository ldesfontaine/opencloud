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
	"github.com/ldesfontaine/opencloud/internal/mail"
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
	// Le relais SMTP du canal e-mail. Le mot de passe vit ici, dans un
	// fichier en 0600 comme la base : l'API ne le renvoie jamais.
	MailHost     string `toml:"mail_host,omitempty"`
	MailPort     int    `toml:"mail_port,omitempty"`
	MailTLS      string `toml:"mail_tls,omitempty"`
	MailUsername string `toml:"mail_username,omitempty"`
	MailPassword string `toml:"mail_password,omitempty"`
	MailFrom     string `toml:"mail_from,omitempty"`
	// La fin de support : coupée tant que l'opérateur ne l'a pas activée.
	// Activée, la machine openCloud lit endoflife.date une fois par jour.
	LifecycleEnabled bool `toml:"lifecycle_enabled,omitempty"`
}

// MailRelay rend le relais réglé ; un port absent suit le mode, un mode
// absent est STARTTLS.
func (s Settings) MailRelay() mail.Relay {
	mode := mail.TLSMode(s.MailTLS)
	if mode == "" {
		mode = mail.TLSStartTLS
	}
	port := s.MailPort
	if port == 0 {
		port = mail.DefaultPortFor(mode)
	}
	return mail.Relay{
		Host: s.MailHost, Port: port, TLS: mode, Username: s.MailUsername, Password: s.MailPassword, From: s.MailFrom,
	}
}

// SetMailRelay écrit le relais ; un relais sans hôte efface tout.
func (s *Settings) SetMailRelay(relay mail.Relay) {
	if relay.Host == "" {
		relay = mail.Relay{}
	}
	s.MailHost, s.MailPort, s.MailTLS = relay.Host, relay.Port, string(relay.TLS)
	s.MailUsername, s.MailPassword, s.MailFrom = relay.Username, relay.Password, relay.From
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
