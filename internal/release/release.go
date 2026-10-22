package release

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/brand"
)

var (
	// ErrNotListed : le nom du fichier n'est pas dans les sommes de la version.
	ErrNotListed = errors.New("release: file not listed in the version")
	// ErrDigestMismatch : la somme du fichier n'est pas celle de la version.
	ErrDigestMismatch = errors.New("release: file does not match the version")
)

// Un fichier de sommes ou de signature tient en quelques centaines d'octets.
const maxSmallFileSize = 1 << 20

// Release est une version dont les sommes sont signées par la clé de confiance.
type Release struct {
	Version Version
	Sums    map[string]string
}

// VerifySums vérifie la signature des sommes, puis lit la version dans le
// commentaire de confiance : c'est la seule partie du fichier de signature
// que la signature couvre en plus des sommes, et la seule source de la version.
func VerifySums(sums, signature []byte, key PublicKey) (Release, error) {
	comment, err := Verify(sums, signature, key)
	if err != nil {
		return Release{}, err
	}
	fields, err := parseTrustedComment(comment)
	if err != nil {
		return Release{}, err
	}
	if fields["file"] != brand.ReleaseSumsFile {
		return Release{}, fmt.Errorf("%w: trusted comment names another file", ErrMalformed)
	}
	version, err := ParseVersion(fields["version"])
	if err != nil {
		return Release{}, err
	}
	parsed, err := ParseSums(sums)
	if err != nil {
		return Release{}, err
	}
	return Release{Version: version, Sums: parsed}, nil
}

// parseTrustedComment lit les paires « clé:valeur » séparées par des tabulations.
func parseTrustedComment(comment string) (map[string]string, error) {
	fields := make(map[string]string)
	for _, pair := range strings.Split(comment, "\t") {
		name, value, found := strings.Cut(pair, ":")
		if !found {
			return nil, fmt.Errorf("%w: trusted comment field %q", ErrMalformed, pair)
		}
		if _, seen := fields[name]; seen {
			return nil, fmt.Errorf("%w: trusted comment field %q twice", ErrMalformed, name)
		}
		fields[name] = value
	}
	return fields, nil
}

// Check lit content en entier et vérifie sa somme contre celle que la version
// donne à name.
func (release Release) Check(name string, content io.Reader) error {
	want, listed := release.Sums[name]
	if !listed {
		return fmt.Errorf("%w: %s", ErrNotListed, name)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, content); err != nil {
		return fmt.Errorf("read %s: %w", name, err)
	}
	if hex.EncodeToString(hash.Sum(nil)) != want {
		return fmt.Errorf("%w: %s", ErrDigestMismatch, name)
	}
	return nil
}

// VerifyFile vérifie que path appartient à la version signée par key : les
// sommes et leur signature se lisent aux chemins donnés, le fichier est
// cherché sous son nom de base.
func VerifyFile(path, sumsPath, signaturePath string, key PublicKey) (Release, error) {
	sums, err := readSmallFile(sumsPath)
	if err != nil {
		return Release{}, err
	}
	signature, err := readSmallFile(signaturePath)
	if err != nil {
		return Release{}, err
	}
	signed, err := VerifySums(sums, signature, key)
	if err != nil {
		return Release{}, err
	}
	file, err := os.Open(path) // #nosec G304 -- chemin donné par l'opérateur
	if err != nil {
		return Release{}, fmt.Errorf("open file: %w", err)
	}
	defer file.Close()
	if err := signed.Check(filepath.Base(path), file); err != nil {
		return Release{}, err
	}
	return signed, nil
}

func readSmallFile(path string) ([]byte, error) {
	file, err := os.Open(path) // #nosec G304 -- chemin donné par l'opérateur
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer file.Close()
	return readSmall(file, filepath.Base(path))
}

// readSmall lit un fichier de sommes ou de signature, borné.
func readSmall(file io.Reader, name string) ([]byte, error) {
	content, err := io.ReadAll(io.LimitReader(file, maxSmallFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	if len(content) > maxSmallFileSize {
		return nil, fmt.Errorf("%w: %s is too long", ErrMalformed, name)
	}
	return content, nil
}
