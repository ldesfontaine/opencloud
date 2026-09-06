package config

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// DefaultPath : l'emplacement Debian d'une configuration de service, celui
// où le paquet .deb pose la sienne. Le dev passe toujours --config.
const DefaultPath = "/etc/opencloud/config.toml"

// Config est la configuration validée, prête à l'emploi.
type Config struct {
	// Adresse d'écoute de l'interface, hôte:port.
	Listen string `toml:"listen"`
	// Répertoire d'état : base SQLite, sauvegardes, clés, jetons. Obligatoire.
	// Relatif au dossier du fichier de configuration s'il n'est pas absolu ;
	// Load le rend absolu.
	StateDir string `toml:"state_dir"`
	// Développement seulement : garde admin / opencloud sans obliger le
	// changement à la première connexion. Faux par défaut, donc en production.
	AllowDefaultPassword bool `toml:"allow_default_password"`
	// Longueur minimale d'un nouveau mot de passe, en caractères. 12 par
	// défaut (OWASP, compte d'administration) ; 0 lève la règle, pour un
	// réseau de confiance (08-securite-et-secrets.md).
	MinPasswordLength int `toml:"min_password_length"`
	// Jeton GitHub en lecture seule pour self-update, nécessaire tant que le
	// dépôt des releases est privé. Vide sinon.
	GitHubToken string `toml:"github_token"`
}

// Bornes de min_password_length : au-delà, plus personne ne se connecte.
const (
	DefaultMinPasswordLength = 12
	maxMinPasswordLength     = 64
)

// Un jeton GitHub tient sur une ligne, en ASCII imprimable.
const maxGitHubTokenLength = 255

// Ce qui a une valeur par défaut. state_dir n'en a pas : dire où vit l'état
// est le rôle du fichier, pas du code.
func defaults() Config {
	return Config{
		Listen:            "127.0.0.1:8080",
		MinPasswordLength: DefaultMinPasswordLength,
	}
}

// Warning nomme une clé lue mais inconnue. Le programme démarre quand même :
// une faute de frappe ne doit pas empêcher de réparer l'infrastructure.
type Warning struct {
	Key string
}

func (w Warning) String() string {
	return fmt.Sprintf("clé inconnue ignorée : %s", w.Key)
}

// Load lit, décode et valide le fichier. Les avertissements sont rendus même
// quand la configuration est valide ; l'appelant les journalise.
func Load(path string) (Config, []Warning, error) {
	content, err := os.ReadFile(path) // #nosec G304 -- bounded: chemin donné par l'opérateur en ligne de commande
	if err != nil {
		return Config{}, nil, fmt.Errorf("lire la configuration : %w", err)
	}

	cfg, warnings, err := Parse(content)
	if err != nil {
		return Config{}, nil, fmt.Errorf("%s : %w", path, err)
	}

	cfg.StateDir, err = resolveStateDir(cfg.StateDir, path)
	if err != nil {
		return Config{}, nil, fmt.Errorf("%s : clé « state_dir » : %w", path, err)
	}
	return cfg, warnings, nil
}

// resolveStateDir rend state_dir absolu. Un chemin relatif l'est au dossier du
// fichier de configuration, pas au dossier courant : la même configuration
// marche d'où qu'on lance le binaire, et sur n'importe quelle machine.
func resolveStateDir(stateDir, configPath string) (string, error) {
	if filepath.IsAbs(stateDir) {
		return filepath.Clean(stateDir), nil
	}

	configDir, err := filepath.Abs(filepath.Dir(configPath))
	if err != nil {
		return "", fmt.Errorf("situer le fichier de configuration : %w", err)
	}
	return filepath.Join(configDir, stateDir), nil
}

// Parse décode et valide un contenu TOML, sans résoudre state_dir : il ne
// connaît pas le fichier. Séparé de Load pour les tests.
func Parse(content []byte) (Config, []Warning, error) {
	cfg := defaults()

	decoder := toml.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()

	// Une erreur qui ne porte que des clés inconnues est volontairement
	// laissée passer : le reste est décodé, et elle devient des avertissements.
	err := decoder.Decode(&cfg)
	warnings := collectUnknownKeys(err)
	if err != nil && len(warnings) == 0 {
		return Config{}, nil, fmt.Errorf("configuration illisible : %w", describeDecodeError(err))
	}

	if err := cfg.validate(); err != nil {
		return Config{}, nil, err
	}
	return cfg, warnings, nil
}

// Une clé inconnue fait échouer le décodeur strict, mais le reste de la
// configuration est déjà décodé : on la transforme en avertissement.
func collectUnknownKeys(err error) []Warning {
	var strict *toml.StrictMissingError
	if !errors.As(err, &strict) {
		return nil
	}

	var warnings []Warning
	for _, missing := range strict.Errors {
		warnings = append(warnings, Warning{Key: strings.Join(missing.Key(), ".")})
	}
	return warnings
}

// Une erreur de décodage nomme la clé quand elle en a une (mauvais type),
// sinon la position (syntaxe).
func describeDecodeError(err error) error {
	var decodeErr *toml.DecodeError
	if !errors.As(err, &decodeErr) {
		return err
	}

	row, column := decodeErr.Position()
	if key := decodeErr.Key(); len(key) > 0 {
		return fmt.Errorf("clé « %s » : valeur de mauvais type, ligne %d", strings.Join(key, "."), row)
	}
	return fmt.Errorf("ligne %d, colonne %d : %s", row, column, decodeErr.Error())
}

func (cfg Config) validate() error {
	if err := validateListen(cfg.Listen); err != nil {
		return fmt.Errorf("clé « listen » : %w", err)
	}
	if err := validateStateDir(cfg.StateDir); err != nil {
		return fmt.Errorf("clé « state_dir » : %w", err)
	}
	if cfg.MinPasswordLength < 0 || cfg.MinPasswordLength > maxMinPasswordLength {
		return fmt.Errorf("clé « min_password_length » : attendu entre 0 et %d, reçu %d", maxMinPasswordLength, cfg.MinPasswordLength)
	}
	if err := validateGitHubToken(cfg.GitHubToken); err != nil {
		return fmt.Errorf("clé « github_token » : %w", err)
	}
	return nil
}

func validateGitHubToken(token string) error {
	if len(token) > maxGitHubTokenLength {
		return fmt.Errorf("plus de %d caractères", maxGitHubTokenLength)
	}
	for _, character := range token {
		if character <= ' ' || character > '~' {
			return errors.New("attendu un jeton sur une ligne, sans espace")
		}
	}
	return nil
}

func validateListen(listen string) error {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("attendu hôte:port, reçu %q", listen)
	}
	if port == "" {
		return fmt.Errorf("le port manque dans %q", listen)
	}
	if _, err := net.LookupPort("tcp", port); err != nil {
		return fmt.Errorf("port invalide %q", port)
	}
	if host != "" && host != "localhost" && net.ParseIP(host) == nil {
		return fmt.Errorf("hôte invalide %q, attendu une adresse IP ou localhost", host)
	}
	return nil
}

func validateStateDir(dir string) error {
	if dir == "" {
		return errors.New("obligatoire, absolue ou relative au fichier de configuration")
	}
	return nil
}
