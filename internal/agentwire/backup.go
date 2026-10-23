package agentwire

import (
	"regexp"
	"time"

	"github.com/ldesfontaine/opencloud/internal/brand"
)

// BackupReceiveParams sont les paramètres de backup.receive : le colis à
// télécharger, le dossier où écrire, les fichiers attendus avec leur taille
// et leur somme. Rien ici n'est un secret : le contenu est déjà chiffré.
type BackupReceiveParams struct {
	Parcel string       `json:"parcel"`
	Path   string       `json:"path"`
	Files  []ParcelFile `json:"files"`
}

// BackupPruneParams sont ceux de backup.prune : le dossier, et combien de
// sauvegardes y garder.
type BackupPruneParams struct {
	Path string `json:"path"`
	Keep int    `json:"keep"`
}

const backupFileTimeLayout = "20060102-150405"

var (
	backupFilePattern    = regexp.MustCompile(`^` + regexp.QuoteMeta(brand.BackupFilePrefix) + `\d{8}-\d{6}\.tar\.age$`)
	backupKeyFilePattern = regexp.MustCompile(`^` + regexp.QuoteMeta(brand.BackupKeyFilePrefix) + `[0-9a-f]{16}\.age$`)
)

// BackupFileName nomme la sauvegarde faite à cet instant, en UTC : le tri par
// nom est le tri par date.
func BackupFileName(at time.Time) string {
	return brand.BackupFilePrefix + at.UTC().Format(backupFileTimeLayout) + ".tar.age"
}

// IsBackupFile dit si un nom a la forme exacte d'une sauvegarde : l'élagage
// n'efface rien d'autre (ni une clé déposée, ni un fichier de l'opérateur).
func IsBackupFile(name string) bool {
	return backupFilePattern.MatchString(name)
}

// BackupKeyFileName nomme la clé scellée déposée à côté des sauvegardes
// qu'elle ouvre.
func BackupKeyFileName(fingerprint string) string {
	return brand.BackupKeyFilePrefix + fingerprint + ".age"
}

// IsBackupKeyFile : la forme exacte d'une clé déposée. Avec IsBackupFile, ce
// sont les deux seuls noms que backup.receive écrit.
func IsBackupKeyFile(name string) bool {
	return backupKeyFilePattern.MatchString(name)
}
