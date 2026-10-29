package rollback

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/fsx"
	"github.com/ldesfontaine/opencloud/internal/release"
)

// BinaryName est le nom du binaire d'une version dans bin/.
func BinaryName(version string) string {
	return brand.AgentBinaryPrefix + version
}

// VersionOf rend la version que porte un nom de binaire de bin/ ; faux si ce
// n'est pas « opencloud-vX.Y.Z ».
func VersionOf(binaryName string) (string, bool) {
	version, found := strings.CutPrefix(binaryName, brand.AgentBinaryPrefix)
	if !found {
		return "", false
	}
	if _, err := release.ParseVersion(version); err != nil {
		return "", false
	}
	return version, true
}

// LinkTarget rend le nom du binaire vers lequel un lien de bin/ pointe ; vide
// si le lien manque ou n'en est pas un. Un lien en chemin absolu donne le même
// nom que son équivalent relatif.
func LinkTarget(bin *os.Root, link string) string {
	target, err := bin.Readlink(link)
	if err != nil || target == "" {
		return ""
	}
	return filepath.Base(target)
}

// SwitchLink fait pointer un lien de bin/ sur un binaire : un lien neuf à
// côté, renommé sur l'ancien, donc atomique. Le dossier est écrit sur le
// disque avant de rendre la main, car on redémarre juste après.
func SwitchLink(bin *os.Root, link, target string) error {
	temporary := link + ".new"
	if err := bin.Remove(temporary); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("clear %s: %w", temporary, err)
	}
	if err := bin.Symlink(target, temporary); err != nil {
		return fmt.Errorf("link %s: %w", temporary, err)
	}
	if err := bin.Rename(temporary, link); err != nil {
		_ = bin.Remove(temporary)
		return fmt.Errorf("switch %s: %w", link, err)
	}
	if err := fsx.SyncDir(bin, "."); err != nil {
		return fmt.Errorf("sync bin dir: %w", err)
	}
	return nil
}

// Arm prépare le retour : le lien previous vise la version d'avant, puis le
// marqueur est écrit, tous deux AVANT que le lien current ne bouge. Un
// marqueur sans previous serait un retour qui ne tient pas ; previous sans
// marqueur est sans effet.
func Arm(state, bin *os.Root, marker Marker) error {
	if err := SwitchLink(bin, brand.AgentPreviousLink, BinaryName(marker.Previous)); err != nil {
		return err
	}
	return Save(state, marker)
}
