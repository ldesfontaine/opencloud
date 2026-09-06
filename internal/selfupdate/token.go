package selfupdate

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"syscall"

	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// TokenPath : le jeton GitHub de self-update, hors de portée du service.
// self-update tourne en root ; le service, lui, ne lit que config.toml
// (08-securite-et-secrets.md).
const TokenPath = "/etc/opencloud/github-token" // #nosec G101 -- un chemin de fichier, pas un secret

const (
	// Le fichier tient une ligne : au-delà, ce n'est pas un jeton.
	maxTokenFileBytes = 512

	// Aucun droit pour le groupe ni pour les autres : un secret trop ouvert
	// ne se lit pas.
	tokenAccessForGroupOrOthers = 0o077

	rootUID = 0
)

// Un jeton GitHub : préfixe, soulignés et base62. La forme est vérifiée pour
// refuser tôt un fichier qui n'en contient pas un — un en-tête Authorization
// n'accepte de toute façon rien d'autre.
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9_]{20,255}$`)

// ReadToken lit le jeton du fichier. Absent : pas de jeton, et le dépôt doit
// être public. Lisible par d'autres que root : refus, jamais de lecture.
func ReadToken(path string) (string, error) {
	return readToken(path, rootUID)
}

// ownerUID est un paramètre pour que les tests jouent sans être root.
func readToken(path string, ownerUID uint32) (string, error) {
	// O_NONBLOCK : un tube nommé à cette place bloquerait self-update à
	// l'ouverture au lieu de se faire refuser.
	// #nosec G304 -- bounded: TokenPath, une constante
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", refusal.Refusal{
			Cause:  fmt.Sprintf("%s existe mais n'est pas lisible", path),
			Remedy: fmt.Sprintf("lancer self-update avec sudo, et poser « chmod 0600 %s »", path),
		}
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", refusal.Refusal{
			Cause:  fmt.Sprintf("%s n'est pas un fichier ordinaire", path),
			Remedy: fmt.Sprintf("y écrire le jeton, puis « chmod 0600 %s » et « chown root:root %s »", path, path),
		}
	}
	if err := checkTokenFileAccess(path, info, ownerUID); err != nil {
		return "", err
	}

	content, err := io.ReadAll(io.LimitReader(file, maxTokenFileBytes+1))
	if err != nil {
		return "", refusal.Refusal{
			Cause:  fmt.Sprintf("%s n'a pas pu être lu jusqu'au bout", path),
			Remedy: "vérifier le fichier, puis relancer",
		}
	}
	if len(content) > maxTokenFileBytes {
		return "", refusal.Refusal{
			Cause:  fmt.Sprintf("%s fait plus de %d octets : un jeton tient sur une ligne", path, maxTokenFileBytes),
			Remedy: "n'y laisser que le jeton, seul sur la première ligne, puis relancer",
		}
	}

	token, _, _ := strings.Cut(string(content), "\n")
	token = strings.TrimSpace(token)
	// La valeur n'est jamais recopiée dans un message : c'est un secret.
	if !tokenPattern.MatchString(token) {
		return "", refusal.Refusal{
			Cause:  fmt.Sprintf("la première ligne de %s n'a pas la forme d'un jeton GitHub", path),
			Remedy: "y poser un jeton en lecture (contenu), de 20 à 255 lettres, chiffres ou soulignés",
		}
	}
	return token, nil
}

func checkTokenFileAccess(path string, info os.FileInfo, ownerUID uint32) error {
	if info.Mode().Perm()&tokenAccessForGroupOrOthers != 0 {
		return refusal.Refusal{
			Cause:  fmt.Sprintf("%s est lisible ailleurs que par son propriétaire (mode %04o)", path, info.Mode().Perm()),
			Remedy: fmt.Sprintf("poser « chmod 0600 %s », puis relancer", path),
		}
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != ownerUID {
		return refusal.Refusal{
			Cause:  fmt.Sprintf("%s n'appartient pas à root", path),
			Remedy: fmt.Sprintf("poser « chown root:root %s », puis relancer", path),
		}
	}
	return nil
}
