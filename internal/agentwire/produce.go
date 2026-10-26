package agentwire

import "github.com/ldesfontaine/opencloud/internal/brand"

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
