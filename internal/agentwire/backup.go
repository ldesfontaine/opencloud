package agentwire

import (
	"regexp"
	"strings"
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

// BackupPruneParams sont ceux de backup.prune : le dossier, le préfixe des
// fichiers d'un plan (BackupFilePrefix), et combien de sauvegardes de ce
// préfixe y garder. Sans préfixe l'ordre est refusé : l'élagage ne touche
// que les fichiers d'un plan, jamais ceux des autres.
type BackupPruneParams struct {
	Path   string `json:"path"`
	Prefix string `json:"prefix"`
	Keep   int    `json:"keep"`
}

const (
	backupFileTimeLayout = "20060102-150405"
	backupFileSuffix     = ".tar.age"
	// DatabaseSubject est le sujet du plan de la base : ses fichiers gardent
	// le nom d'avant les plans multiples (opencloud-AAAAMMJJ-HHMMSS.tar.age).
	DatabaseSubject = "database"
	// Le préfixe tient dans un nom de fichier, avec l'instant et le suffixe.
	maxBackupPrefix = 64
)

var (
	backupFilePattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*-\d{8}-\d{6}\.tar\.age$`)
	backupPrefixPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	backupStampPattern   = regexp.MustCompile(`^\d{8}-\d{6}\.tar\.age$`)
	backupKeyFilePattern = regexp.MustCompile(`^` + regexp.QuoteMeta(brand.BackupKeyFilePrefix) + `[0-9a-f]{16}\.age$`)
)

// BackupFilePrefix est ce que les fichiers d'un plan ont en commun : le
// sujet, suivi d'un suffixe quand une source produit plusieurs archives
// (une par machine virtuelle). Le plan de la base garde « opencloud ».
func BackupFilePrefix(subject, suffix string) string {
	prefix := subject
	if subject == DatabaseSubject {
		prefix = brand.ID
	}
	if suffix != "" {
		prefix += "-" + suffix
	}
	return prefix
}

// IsBackupPrefix dit si un texte peut préfixer des fichiers de sauvegarde :
// l'agent n'élague jamais sous un préfixe qui n'a pas cette forme.
func IsBackupPrefix(prefix string) bool {
	return len(prefix) <= maxBackupPrefix && backupPrefixPattern.MatchString(prefix)
}

// BackupFileName nomme la sauvegarde du plan faite à cet instant, en UTC : le
// tri par nom est le tri par date.
func BackupFileName(subject string, at time.Time) string {
	return BackupFilePrefix(subject, "") + "-" + at.UTC().Format(backupFileTimeLayout) + backupFileSuffix
}

// IsBackupFile dit si un nom a la forme d'une sauvegarde : un préfixe, un
// instant, « .tar.age ». L'élagage n'efface rien d'autre (ni une clé
// déposée, ni un fichier de l'opérateur).
func IsBackupFile(name string) bool {
	return backupFilePattern.MatchString(name)
}

// IsBackupFileOf dit si la sauvegarde appartient à ce préfixe : ce qui suit
// le préfixe est exactement l'instant. « pve » ne prend donc jamais
// « pve-vm-100-… », dont le reste n'est pas un instant. Un préfixe qui n'en
// est pas un (vide, une majuscule, un chemin) ne prend rien.
func IsBackupFileOf(name, prefix string) bool {
	if !IsBackupPrefix(prefix) {
		return false
	}
	rest, found := strings.CutPrefix(name, prefix+"-")
	return found && backupStampPattern.MatchString(rest)
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
