package agentwire

import "net/url"

// ParcelRoute est le préfixe où l'agent télécharge un colis : un événement
// du flux est borné (MaxEventBytes), une base fait des dizaines de Mio. Le
// serveur le sert sous le jeton de session du flux de l'agent.
const ParcelRoute = "/agent/parcels/"

// ParcelFile est un fichier du colis tel que l'ordre signé le décrit : la
// taille et la somme que l'agent vérifie, que le serveur a calculées en
// l'écrivant.
type ParcelFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// ParcelPath rend le chemin d'un fichier du colis, tel que l'agent le
// demande.
func ParcelPath(parcel, name string) string {
	return ParcelRoute + url.PathEscape(parcel) + "/" + url.PathEscape(name)
}
