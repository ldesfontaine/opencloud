package registry

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Credentials sont ce qu'un registre privé demande ; vides, on y va
// anonyme.
type Credentials struct {
	Username string
	Password string
}

// Keychain rend les identifiants d'un registre, s'il en a.
type Keychain interface {
	Lookup(registry string) (Credentials, bool)
}

// Static est un trousseau en mémoire, par hôte de registre.
type Static map[string]Credentials

func (s Static) Lookup(registry string) (Credentials, bool) {
	credentials, ok := s[registry]
	return credentials, ok
}

// File est un trousseau relu à chaque demande : le fichier de Docker
// peut changer après un « docker login », sans redémarrer l'agent.
type File struct {
	Path string
}

func (f File) Lookup(registry string) (Credentials, bool) {
	keychain, err := LoadDockerConfig(f.Path)
	if err != nil {
		return Credentials{}, false
	}
	return keychain.Lookup(registry)
}

// DefaultConfigPath est le config.json de Docker pour l'utilisateur qui
// fait tourner l'agent : root, donc /root/.docker, sauf DOCKER_CONFIG.
func DefaultConfigPath() string {
	if dir := os.Getenv("DOCKER_CONFIG"); dir != "" {
		return filepath.Join(dir, "config.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".docker", "config.json")
}

// Le fichier de Docker : « auths » porte, par registre, soit « auth » en
// base64 de « user:password », soit les deux champs à part. Ce que
// « credsStore » et « credHelpers » délèguent à un binaire n'est pas lu.
type dockerConfig struct {
	Auths map[string]struct {
		Auth     string `json:"auth"`
		Username string `json:"username"`
		Password string `json:"password"`
	} `json:"auths"`
}

// LoadDockerConfig lit les identifiants en clair du fichier ; un fichier
// absent est un trousseau vide, pas une erreur.
func LoadDockerConfig(path string) (Static, error) {
	keychain := Static{}
	if path == "" {
		return keychain, nil
	}
	content, err := os.ReadFile(path) // #nosec G304 -- le config.json de Docker, chemin du produit ou de DOCKER_CONFIG.
	if errors.Is(err, fs.ErrNotExist) {
		return keychain, nil
	}
	if err != nil {
		return nil, err
	}
	var config dockerConfig
	if err := json.Unmarshal(content, &config); err != nil {
		return nil, err
	}
	for host, entry := range config.Auths {
		credentials := Credentials{Username: entry.Username, Password: entry.Password}
		if entry.Auth != "" {
			decoded, err := base64.StdEncoding.DecodeString(entry.Auth)
			if err != nil {
				continue
			}
			credentials.Username, credentials.Password, _ = strings.Cut(string(decoded), ":")
		}
		if credentials.Username == "" {
			continue
		}
		keychain[normalizeHost(host)] = credentials
	}
	return keychain, nil
}

// normalizeHost ramène « https://index.docker.io/v1/ » à l'hôte que l'API
// v2 sert : Docker écrit sa clé du Hub à l'ancienne.
func normalizeHost(host string) string {
	host = strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://")
	host, _, _ = strings.Cut(host, "/")
	if host == "index.docker.io" || host == "docker.io" {
		return DockerHub
	}
	return host
}
