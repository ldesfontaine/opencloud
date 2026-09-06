package enroll

import (
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/crypto/ssh"
)

// La forme qu'affiche « ssh-keygen -lf » : SHA256 puis les 43 caractères de
// base64 des 32 octets de l'empreinte, sans remplissage.
var fingerprintPattern = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{43}$`)

// hostKey est une clé d'hôte relevée sur la machine : de quoi écrire une ligne
// de known_hosts, et de quoi la comparer à ce que l'opérateur a saisi.
type hostKey struct {
	Algorithm   string
	Encoded     string
	Fingerprint string
}

// Fingerprint rend l'empreinte d'une ligne de clé publique, telle que
// « ssh-keygen -lf » l'écrit et telle que l'opérateur la recopie.
func Fingerprint(hostKeyLine string) (string, error) {
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(hostKeyLine))
	if err != nil {
		return "", fmt.Errorf("lire la clé d'hôte : %w", err)
	}
	return ssh.FingerprintSHA256(key), nil
}

// parseHostKeys lit ce que ssh-keyscan a répondu. Ses commentaires et ses
// lignes de bannière sont jetés ; ce qui ne se relit pas comme une clé n'en
// est pas une, et n'entre pas dans known_hosts.
func parseHostKeys(output string) []hostKey {
	var keys []hostKey
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		algorithm, encoded := fields[1], fields[2]
		fingerprint, err := Fingerprint(algorithm + " " + encoded)
		if err != nil {
			continue
		}
		keys = append(keys, hostKey{Algorithm: algorithm, Encoded: encoded, Fingerprint: fingerprint})
	}
	return keys
}

// knownHostsFor écrit toutes les clés relevées pour cette adresse : plusieurs
// algorithmes, un seul hôte. Rien n'est appris à la connexion.
func knownHostsFor(keys []hostKey, address string, port int) string {
	pattern := hostPattern(address, port)

	var content strings.Builder
	for _, key := range keys {
		content.WriteString(pattern + " " + key.Algorithm + " " + key.Encoded + "\n")
	}
	return content.String()
}
