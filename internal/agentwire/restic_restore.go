package agentwire

import (
	"errors"
	"regexp"
	"strings"
)

// KindResticRestore : la machine SOURCE d'une sauvegarde restaure un fichier ou
// un dossier d'un instantané À CÔTÉ de l'original : <chemin>.restaure-<horodatage>.
// Jamais par-dessus, jamais de suppression : si la destination existe, l'ordre
// est refusé. L'ordre ne porte aucun secret : la source relit le mot de passe du
// dépôt dans le fichier de son lien (0600), qu'elle détient déjà pour
// sauvegarder ; il ne voyage donc pas, et ne peut fuir ni de l'ordre, ni de
// l'audit, ni d'une réponse.
const KindResticRestore Kind = "restic.restore"

// Le suffixe que prend la destination, avant l'horodatage.
const RestoreSuffix = ".restaure-"

var (
	errResticRestore = errors.New("restic restore: request outside its form")
	stampPattern     = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}$`)
)

// ResticRestoreParams : la cible du lien (le dépôt), l'instantané, le chemin
// dans l'instantané, et l'horodatage AAAAMMJJ-HHMMSS que prend la destination.
// L'écran l'affiche avant de confirmer : le chemin exact est connu d'avance.
type ResticRestoreParams struct {
	Target   string `json:"target"`
	Snapshot string `json:"snapshot"`
	Path     string `json:"path"`
	Stamp    string `json:"stamp"`
}

func (p ResticRestoreParams) Validate() error {
	if !IsResticUser(p.Target) {
		return errResticTarget
	}
	if !IsSnapshotID(p.Snapshot) || p.Path == "/" || !IsBrowsePath(p.Path) || !stampPattern.MatchString(p.Stamp) {
		return errResticRestore
	}
	// La destination est un chemin comme un autre : elle reste propre et d'une longueur permise.
	if !IsBrowsePath(RestoreDestination(p.Path, p.Stamp)) {
		return errResticRestore
	}
	return nil
}

// RestoreDestination : le chemin de la copie restaurée, à côté de l'original.
func RestoreDestination(path, stamp string) string {
	return strings.TrimSuffix(path, "/") + RestoreSuffix + stamp
}
