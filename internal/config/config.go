package config

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-playground/validator/v10"
	"github.com/pelletier/go-toml/v2"

	"github.com/ldesfontaine/opencloud/internal/auth"
	"github.com/ldesfontaine/opencloud/internal/brand"
)

const (
	DefaultPath     = brand.ConfigPath
	defaultListen   = "127.0.0.1:8080"
	defaultStateDir = brand.StateDir
	// Une liste d'identités se lit à l'œil : au-delà, c'est un annuaire, et
	// c'est au mandataire de le tenir.
	maxForwardUsers = 64
)

type Config struct {
	// Adresse d'écoute du serveur web, hôte:port.
	Listen string `toml:"listen" validate:"required,hostname_port"`
	// Répertoire d'état : la base SQLite et le cache lifecycle.json.
	StateDir string `toml:"state_dir" validate:"required"`
	// Adresse à laquelle les machines joignent openCloud, mise dans la
	// commande d'installation. Vide : déduite de la requête.
	PublicURL string `toml:"public_url" validate:"omitempty,http_url"`
	// Adresse de mcp-maison, le serveur MCP installé à côté : Paramètres lit
	// son /health. Vide : openCloud ne le cherche pas et marche sans lui.
	MCPURL string `toml:"mcp_url" validate:"omitempty,http_url"`
	// Mandataires dont les en-têtes X-Forwarded-* sont crus, en CIDR.
	TrustedProxies []string `toml:"trusted_proxies" validate:"dive,cidr"`
	// Niveau du journal : debug, info, warn, error. Vide : info. En debug,
	// chaque requête HTTP fait une ligne.
	LogLevel string `toml:"log_level" validate:"omitempty,oneof=debug info warn error"`
	// Fichier d'une ligne qui porte le secret partagé avec le mandataire
	// d'authentification (32 caractères au moins, illisible pour les autres
	// comptes). En mode forward, l'identité n'est crue que d'une requête qui
	// porte ce secret dans l'en-tête X-OpenCloud-Proxy-Secret. Vide : l'adresse
	// du pair seule, ce qu'une boucle locale de confiance ne prouve pas.
	ForwardSecretFile string `toml:"forward_secret_file"`
	// Identités que le mandataire peut transmettre et qu'openCloud admet, en
	// mode forward : une seconde serrure derrière le SSO. Comparaison exacte,
	// casse comprise. Vide : toute identité transmise est admise.
	ForwardUsers []string `toml:"forward_users"`
}

func Default() Config {
	return Config{
		Listen:   defaultListen,
		StateDir: defaultStateDir,
	}
}

// SlogLevel traduit le réglage pour slog ; la validation a déjà borné les
// valeurs, tout le reste vaut info.
func (c Config) SlogLevel() slog.Level {
	switch c.LogLevel {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
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
	warnings = append(warnings, wideProxyWarnings(cfg)...)
	return cfg, warnings, nil
}

// Un mandataire se déclare seul : tout pair d'un préfixe plus large est cru
// pour X-Forwarded-* et, en mode forward, pour l'identité de l'opérateur.
// Un avertissement, pas un refus : derrière plusieurs mandataires à IP
// variable, l'opérateur peut élargir sciemment.
func wideProxyWarnings(cfg Config) []string {
	var warnings []string
	for _, prefix := range cfg.TrustedPrefixes() {
		if prefix.Bits() < prefix.Addr().BitLen() {
			warnings = append(warnings, fmt.Sprintf("trusted proxy %s covers more than one address: every peer in it is believed for X-Forwarded-* and, in forward mode, for the operator identity; list the proxy alone (/32 or /128)", prefix))
		}
	}
	return warnings
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
	if err := validateForwardUsers(cfg.ForwardUsers); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}
	return nil
}

// validateForwardUsers refuse toute liste que l'opérateur n'a pas pu vouloir.
// Une entrée vide ou à espaces en bord ne correspondrait jamais à une identité
// (le serveur rogne celle qu'il reçoit) : la laisser passer fermerait la porte
// à tous sans que rien ne le dise. Les doublons sont refusés pour la même
// raison : une liste qu'on relit doit dire ce qu'elle fait.
func validateForwardUsers(users []string) error {
	if len(users) > maxForwardUsers {
		return fmt.Errorf("forward_users: %d entries, at most %d", len(users), maxForwardUsers)
	}
	seen := make(map[string]bool, len(users))
	for _, user := range users {
		if err := validateForwardUser(user); err != nil {
			return fmt.Errorf("forward_users: entry %q: %w", user, err)
		}
		if seen[user] {
			return fmt.Errorf("forward_users: entry %q listed twice", user)
		}
		seen[user] = true
	}
	return nil
}

func validateForwardUser(user string) error {
	if user == "" {
		return errors.New("empty")
	}
	if !utf8.ValidString(user) {
		return errors.New("not valid UTF-8")
	}
	if utf8.RuneCountInString(user) > auth.MaxUsernameLength {
		return fmt.Errorf("longer than %d characters", auth.MaxUsernameLength)
	}
	if strings.TrimSpace(user) != user {
		return errors.New("leading or trailing space")
	}
	for _, character := range user {
		if unicode.IsControl(character) {
			return errors.New("control character")
		}
	}
	return nil
}
