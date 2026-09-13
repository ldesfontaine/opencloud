package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"

	"github.com/go-playground/validator/v10"
	"github.com/pelletier/go-toml/v2"
)

const (
	DefaultPath     = "/etc/opencloud/config.toml"
	defaultListen   = "127.0.0.1:8080"
	defaultStateDir = "/var/lib/opencloud"
)

type Config struct {
	// Adresse d'écoute du serveur web, hôte:port.
	Listen string `toml:"listen" validate:"required,hostname_port"`
	// Répertoire d'état : base, certificats, sauvegardes locales.
	StateDir string `toml:"state_dir" validate:"required"`
	// Adresse à laquelle les machines joignent openCloud, mise dans la
	// commande d'installation. Vide : déduite de la requête.
	PublicURL string `toml:"public_url" validate:"omitempty,http_url"`
	// Mandataires dont les en-têtes X-Forwarded-* sont crus, en CIDR.
	TrustedProxies []string `toml:"trusted_proxies" validate:"dive,cidr"`
}

func Default() Config {
	return Config{
		Listen:   defaultListen,
		StateDir: defaultStateDir,
	}
}

// TrustedPrefixes rend les mandataires sous forme utilisable ; la validation
// a déjà garanti la syntaxe.
func (c Config) TrustedPrefixes() []netip.Prefix {
	prefixes := make([]netip.Prefix, 0, len(c.TrustedProxies))
	for _, cidr := range c.TrustedProxies {
		if prefix, err := netip.ParsePrefix(cidr); err == nil {
			prefixes = append(prefixes, prefix)
		}
	}
	return prefixes
}

// Lit le fichier, le valide, et renvoie les clés inconnues en avertissements.
func Load(path string) (Config, []string, error) {
	content, err := readFile(path)
	if err != nil {
		return Config{}, nil, fmt.Errorf("read config: %w", err)
	}
	cfg, warnings, err := Parse(content)
	if err != nil {
		return Config{}, nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, warnings, nil
}

// Le chemin vient de la ligne de commande ; il est lu via os.Root, borné à
// son propre dossier, comme tout accès fichier du projet.
func readFile(path string) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.ReadFile(filepath.Base(path))
}

func Parse(content []byte) (Config, []string, error) {
	cfg := Default()
	warnings, err := decode(content, &cfg)
	if err != nil {
		return Config{}, nil, err
	}
	if err := validate(cfg); err != nil {
		return Config{}, nil, err
	}
	return cfg, warnings, nil
}

// Décode en mode strict pour repérer les clés inconnues, puis les signale
// sans bloquer : la struct est déjà remplie avec les clés connues.
func decode(content []byte, cfg *Config) ([]string, error) {
	err := toml.NewDecoder(bytes.NewReader(content)).DisallowUnknownFields().Decode(cfg)
	if err == nil {
		return nil, nil
	}
	var strict *toml.StrictMissingError
	if !errors.As(err, &strict) {
		return nil, err
	}
	warnings := make([]string, 0, len(strict.Errors))
	for _, missing := range strict.Errors {
		warnings = append(warnings, fmt.Sprintf("unknown key %q ignored", missing.Key()))
	}
	return warnings, nil
}

func validate(cfg Config) error {
	checker := validator.New(validator.WithRequiredStructEnabled())
	if err := checker.Struct(cfg); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}
	return nil
}
