package agentwire

import (
	"regexp"
	"time"

	"github.com/ldesfontaine/opencloud/internal/brand"
)

// BackupProduceParams sont les paramètres de backup.produce : quel producteur
// lancer, sur quelle cible, sous quel préfixe nommer l'archive, pour quelle
// clé publique la chiffrer, et dans quel colis (créé par le serveur pour la
// machine de destination) la téléverser. Rien ici n'est un secret : la clé
// publique age ne chiffre que, et le contenu part déjà chiffré.
type BackupProduceParams struct {
	Producer  string `json:"producer"`
	Target    string `json:"target"`
	Prefix    string `json:"prefix"`
	Recipient string `json:"recipient"`
	Parcel    string `json:"parcel"`
}

// HeaderParcelSHA256 porte la somme du fichier qu'un agent téléverse : le
// serveur la compare à ce qu'il a reçu.
const HeaderParcelSHA256 = brand.HeaderPrefix + "SHA256"

// Le nom d'une archive produite est un préfixe, un instant UTC et « .tar.age »,
// comme celui d'une sauvegarde de la base : le tri par nom est le tri par date.
// Le préfixe tient dans un nom de fichier avec le reste.
const (
	producedFileTimeLayout = "20060102-150405"
	maxProducePrefix       = 64
)

var (
	producedFilePattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*-\d{8}-\d{6}\.tar\.age$`)
	producePrefixPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

// ProducedFileName nomme l'archive que le préfixe produit à cet instant.
func ProducedFileName(prefix string, at time.Time) string {
	return prefix + "-" + at.UTC().Format(producedFileTimeLayout) + ".tar.age"
}

// IsProducedFile dit si un nom a la forme d'une archive produite : un
// préfixe, un instant, « .tar.age ». Le serveur n'en range pas d'autre dans un
// colis téléversé.
func IsProducedFile(name string) bool {
	return producedFilePattern.MatchString(name)
}

// IsProducePrefix dit si un texte peut préfixer une archive produite.
func IsProducePrefix(prefix string) bool {
	return len(prefix) <= maxProducePrefix && producePrefixPattern.MatchString(prefix)
}
