package restic

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/fsx"
)

// Le coût bcrypt de htpasswd -B. rest-server garde en cache les vérifications
// réussies : le coût ne se paie pas à chaque requête.
const bcryptCost = 10

// Le fichier htpasswd tient une ligne par source liée ; il se lit en entier.
const maxHtpasswdBytes = 64 << 10

// Le préfixe qu'écrit Go et celui qu'écrit htpasswd -B : même algorithme,
// rest-server lit les deux.
const (
	goBcryptPrefix       = "$2a$"
	htpasswdBcryptPrefix = "$2y$"
)

var (
	errHtpasswdUser = errors.New("htpasswd: user name invalid")
	errHtpasswdHash = errors.New("htpasswd: not a bcrypt hash")
	errHtpasswdLine = errors.New("htpasswd: line unreadable")
)

// HashPassword rend l'empreinte bcrypt du mot de passe REST, au format de
// htpasswd -B. La cible la calcule du mot de passe reçu par restic.user.add ;
// seule l'empreinte entre dans le fichier.
func HashPassword(password []byte) (string, error) {
	hash, err := bcrypt.GenerateFromPassword(password, bcryptCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return strings.Replace(string(hash), goBcryptPrefix, htpasswdBcryptPrefix, 1), nil
}

// PasswordMatches dit si une empreinte est celle de ce mot de passe : une
// ligne déjà juste ne se réécrit pas (bcrypt sale, l'empreinte changerait).
func PasswordMatches(hash string, password []byte) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), password) == nil
}

// HtpasswdEntry est une ligne du fichier : l'utilisateur, donc le dépôt, et
// l'empreinte de son mot de passe.
type HtpasswdEntry struct {
	User string
	Hash string
}

func (e HtpasswdEntry) validate() error {
	if !agentwire.IsResticUser(e.User) {
		return errHtpasswdUser
	}
	if !agentwire.IsBcryptHash(e.Hash) {
		return errHtpasswdHash
	}
	return nil
}

// ParseHtpasswd lit le fichier ; une ligne illisible le rend illisible en
// entier, au lieu de perdre un utilisateur à la prochaine écriture.
func ParseHtpasswd(content []byte) ([]HtpasswdEntry, error) {
	var entries []HtpasswdEntry
	for line := range strings.Lines(string(content)) {
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}
		user, hash, hasColon := strings.Cut(line, ":")
		entry := HtpasswdEntry{User: user, Hash: hash}
		if !hasColon || entry.validate() != nil {
			return nil, errHtpasswdLine
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// SetHtpasswdUser ajoute l'utilisateur, ou remplace son empreinte.
func SetHtpasswdUser(entries []HtpasswdEntry, added HtpasswdEntry) ([]HtpasswdEntry, error) {
	if err := added.validate(); err != nil {
		return nil, err
	}
	kept := RemoveHtpasswdUser(entries, added.User)
	return append(kept, added), nil
}

// RemoveHtpasswdUser retire l'utilisateur ; ses données restent sur la cible.
func RemoveHtpasswdUser(entries []HtpasswdEntry, user string) []HtpasswdEntry {
	return slices.DeleteFunc(slices.Clone(entries), func(entry HtpasswdEntry) bool {
		return entry.User == user
	})
}

// FormatHtpasswd rend le contenu du fichier, trié par utilisateur pour qu'une
// réécriture sans changement donne les mêmes octets.
func FormatHtpasswd(entries []HtpasswdEntry) ([]byte, error) {
	sorted := slices.Clone(entries)
	slices.SortFunc(sorted, func(a, b HtpasswdEntry) int {
		return strings.Compare(a.User, b.User)
	})
	var content strings.Builder
	for _, entry := range sorted {
		if err := entry.validate(); err != nil {
			return nil, err
		}
		content.WriteString(entry.User + ":" + entry.Hash + "\n")
	}
	return []byte(content.String()), nil
}

// ReadHtpasswd lit le fichier sous root ; absent, il est vide.
func ReadHtpasswd(root *os.Root, name string) ([]HtpasswdEntry, error) {
	content, err := root.ReadFile(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read htpasswd: %w", err)
	}
	if len(content) > maxHtpasswdBytes {
		return nil, errHtpasswdLine
	}
	return ParseHtpasswd(content)
}

// WriteHtpasswd remplace le fichier d'un seul coup : rest-server, relu par
// SIGHUP, voit l'ancien ou le nouveau, jamais une ligne coupée.
func WriteHtpasswd(root *os.Root, name string, entries []HtpasswdEntry) error {
	content, err := FormatHtpasswd(entries)
	if err != nil {
		return err
	}
	if err := fsx.WriteAtomic(root, name, content, 0o600); err != nil {
		return fmt.Errorf("write htpasswd: %w", err)
	}
	return nil
}
