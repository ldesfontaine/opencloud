package catalog

import (
	"errors"
	"fmt"

	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// paramZone : la zone dont le jeton est posé. L'interface la propose dans une
// liste, jamais en saisie libre : openCloud ne tient que les zones qu'il
// connaît.
const paramZone = "zone"

// dnsTokenFileName : le jeton, déposé sous files/ et posé par le script à
// côté des certificats qu'il sert à obtenir. Jamais dans params.env, jamais
// dans une ligne de commande.
const dnsTokenFileName = proxyTokenFileName

// dnsTokenFileMode : ce fichier porte un secret, et Traefik seul le lit.
const dnsTokenFileMode = 0o600

// ErrNoTokens : le catalogue a été monté sans source de jetons. Une faute de
// câblage, jamais une saisie.
var ErrNoTokens = errors.New("catalog has no zone token source")

// Tokens rend le fichier de jeton d'une zone, tel qu'il sera déposé. Le vrai
// est *zone.Keeper ; catalog ne garde rien de ce qu'il lit.
type Tokens interface {
	ZoneToken(zone string) ([]byte, error)
}

// DNSTokenZoneOf lit la zone dans les paramètres validés d'une action. Le nom
// du paramètre ne vit qu'ici.
func DNSTokenZoneOf(params map[string]string) string {
	return params[paramZone]
}

// DNSTokenParams rend la zone sous la forme que l'action attend, pour
// pré-remplir son écran « avant ».
func DNSTokenParams(zone string) map[string]string {
	if zone == "" {
		return map[string]string{}
	}
	return map[string]string{paramZone: zone}
}

// DNSTokenPath : le fichier que Traefik lit sur la machine.
func DNSTokenPath() string {
	return proxyAcmeDir + "/" + proxyTokenFileName
}

// dnsTokenFiles dépose le jeton de la zone. Secret : l'écran « avant » dit
// qu'il sera posé, il ne montre pas ce qu'il contient.
func dnsTokenFiles(params map[string]string, tokens Tokens) ([]File, error) {
	zone := params[paramZone]
	if zone == "" {
		// validateParams a déjà refusé une zone vide ; ceci ferme le cas où
		// l'appelant construit un Prepared à la main.
		return nil, refusal.Refusal{
			Cause:  "aucune zone n'a été choisie",
			Remedy: "choisir la zone dont le jeton doit être posé",
		}
	}
	if tokens == nil {
		return nil, fmt.Errorf("%w: %s", ErrNoTokens, zone)
	}

	content, err := tokens.ZoneToken(zone)
	if err != nil {
		return nil, err
	}
	return []File{{
		Path:    dnsTokenFileName,
		Content: content,
		Mode:    dnsTokenFileMode,
		Secret:  true,
	}}, nil
}
