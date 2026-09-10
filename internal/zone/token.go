package zone

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/fsx"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// Où les jetons vivent, sous le répertoire d'état : /var/lib/opencloud/zones
// en production, dev/state/zones en développement.
const (
	tokenDirName   = "zones"
	tokenSuffix    = ".token"
	tokenDirMode   = 0o700
	tokenFileMode  = 0o600
	maxTokenLength = 4096
)

// FingerprintLength : ce que le script écrit et ce qu'on compare. Douze
// caractères hexadécimaux suffisent à distinguer deux jetons, et se lisent.
const FingerprintLength = 12

// ErrTokenMissing : la zone est enregistrée mais son fichier de jeton a
// disparu. Une faute d'exploitation, jamais une saisie.
var ErrTokenMissing = errors.New("zone token file is missing")

// tokenPath rend le chemin du jeton dans le répertoire d'état. Le nom de zone
// a été validé par validate.Domain : ni barre oblique, ni point seul.
func tokenPath(zone string) string {
	return tokenDirName + "/" + zone + tokenSuffix
}

// tokenContent : ce que le fichier porte, sur la machine openCloud comme sur
// les machines. Un saut de ligne final, parce que c'est un fichier texte ;
// lego enlève les blancs de bout avant d'appeler Cloudflare.
func tokenContent(token string) []byte {
	return []byte(token + "\n")
}

// Fingerprint rend l'empreinte SHA-256 du contenu d'un fichier de jeton,
// tronquée. Le script en calcule une identique sur la machine, sur les mêmes
// octets : c'est ce qui relie une pose à une zone.
func Fingerprint(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])[:FingerprintLength]
}

// ZoneToken rend le fichier de jeton d'une zone, tel qu'il sera déposé sous
// files/ par le catalogue. Une zone inconnue est un refus nommé : c'est
// l'opérateur qui a choisi une zone qu'openCloud ne tient pas.
func (k *Keeper) ZoneToken(zone string) ([]byte, error) {
	content, err := k.readToken(zone)
	if errors.Is(err, os.ErrNotExist) {
		return nil, refusal.Refusal{
			Cause:  fmt.Sprintf("openCloud ne tient aucun jeton pour la zone « %s »", zone),
			Remedy: "ajouter la zone et son jeton dans la section « Zones Cloudflare » de la vue Domaines",
		}
	}
	if err != nil {
		return nil, err
	}
	return content, nil
}

func (k *Keeper) readToken(zone string) ([]byte, error) {
	file, err := k.root.Open(tokenPath(zone))
	if err != nil {
		return nil, err
	}
	defer file.Close()

	content, err := io.ReadAll(io.LimitReader(file, maxTokenLength))
	if err != nil {
		return nil, fmt.Errorf("read zone token: %w", err)
	}
	return content, nil
}

// writeToken remplace le fichier de jeton d'une zone, atomiquement, en 0600.
func (k *Keeper) writeToken(zone, token string) error {
	if err := k.root.MkdirAll(tokenDirName, tokenDirMode); err != nil {
		return fmt.Errorf("create zone token directory: %w", err)
	}

	name := tokenPath(zone)
	if err := fsx.WriteFile(k.root, name, tokenContent(token), tokenFileMode); err != nil {
		return fmt.Errorf("write zone token: %w", err)
	}
	// Le mode passé à la création est filtré par l'umask du processus : on le
	// repose, sinon un umask permissif laisserait le jeton lisible.
	if err := k.root.Chmod(name, tokenFileMode); err != nil {
		return fmt.Errorf("protect zone token: %w", err)
	}
	return nil
}

func (k *Keeper) removeToken(zone string) error {
	if err := k.root.Remove(tokenPath(zone)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove zone token: %w", err)
	}
	return nil
}

// refuseEmptyToken : un jeton vide ne se vérifie pas, il se refuse. Les blancs
// de bout viennent d'un copier-coller et ne comptent pas.
func cleanToken(token string) (string, error) {
	cleaned := strings.TrimSpace(token)
	if cleaned == "" {
		return "", refusal.Refusal{
			Cause:  "le jeton saisi est vide",
			Remedy: "coller le jeton d'API que Cloudflare a affiché à sa création",
		}
	}
	if len(cleaned) > maxTokenLength || strings.ContainsAny(cleaned, "\n\r") {
		return "", refusal.Refusal{
			Cause:  "le jeton saisi n'a pas la forme d'un jeton d'API Cloudflare",
			Remedy: "coller le jeton seul, sur une ligne, sans rien autour",
		}
	}
	return cleaned, nil
}
