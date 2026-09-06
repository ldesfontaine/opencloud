package enroll

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

// Une clé publique d'hôte tient sur une ligne courte ; au-delà, le fichier
// n'est pas ce qu'on croit.
const maxHostKeyBytes = 4 << 10

// generateIdentity crée la paire de la machine. La clé privée sort au format
// OpenSSH, celui que ssh -i lit sans conversion.
func generateIdentity(machineID string) (privateKey []byte, publicLine string, err error) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", fmt.Errorf("engendrer la clé : %w", err)
	}

	block, err := ssh.MarshalPrivateKey(private, comment(machineID))
	if err != nil {
		return nil, "", fmt.Errorf("encoder la clé privée : %w", err)
	}
	line, err := authorizedLine(public, machineID)
	if err != nil {
		return nil, "", err
	}
	return pem.EncodeToMemory(block), line, nil
}

// publicLineOf relit la clé publique d'une clé privée déjà posée : elle n'est
// jamais remplacée, donc c'est elle qui fait foi.
func publicLineOf(privateKey []byte, machineID string) (string, error) {
	parsed, err := ssh.ParseRawPrivateKey(privateKey)
	if err != nil {
		return "", fmt.Errorf("relire la clé privée : %w", err)
	}
	// x/crypto rend un pointeur ; d'autres versions la valeur.
	var private ed25519.PrivateKey
	switch key := parsed.(type) {
	case *ed25519.PrivateKey:
		private = *key
	case ed25519.PrivateKey:
		private = key
	default:
		return "", fmt.Errorf("la clé de %s n'est pas une clé Ed25519", machineID)
	}

	public, ok := private.Public().(ed25519.PublicKey)
	if !ok {
		return "", fmt.Errorf("la clé de %s n'a pas de partie publique Ed25519", machineID)
	}
	return authorizedLine(public, machineID)
}

func authorizedLine(public ed25519.PublicKey, machineID string) (string, error) {
	sshKey, err := ssh.NewPublicKey(public)
	if err != nil {
		return "", fmt.Errorf("encoder la clé publique : %w", err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshKey))) + " " + comment(machineID) + "\n", nil
}

func comment(machineID string) string {
	return AccountName + "@" + machineID
}

// knownHostsContent dérive le fichier des clés d'hôte présentes sur la
// machine. Rien n'est appris à la connexion : ce fichier est écrit, pas rempli.
func knownHostsContent(hostKeyDir, address string, port int) (string, error) {
	entries, err := os.ReadDir(hostKeyDir) // bounded: chemin dérivé de la racine système
	if err != nil {
		return "", fmt.Errorf("lire les clés d'hôte de %s : %w", hostKeyDir, err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "ssh_host_") || !strings.HasSuffix(entry.Name(), "_key.pub") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	lines := &strings.Builder{}
	for _, name := range names {
		line, err := hostKeyLine(filepath.Join(hostKeyDir, name), address, port)
		if err != nil {
			return "", err
		}
		lines.WriteString(line)
	}
	if lines.Len() == 0 {
		return "", fmt.Errorf("aucune clé d'hôte dans %s", hostKeyDir)
	}
	return lines.String(), nil
}

func hostKeyLine(path, address string, port int) (string, error) {
	file, err := os.Open(path) // #nosec G304 -- bounded: nom listé dans /etc/ssh, motif ssh_host_*_key.pub
	if err != nil {
		return "", fmt.Errorf("lire %s : %w", path, err)
	}
	defer file.Close()

	content := make([]byte, maxHostKeyBytes)
	read, err := file.Read(content)
	if err != nil && read == 0 {
		return "", fmt.Errorf("lire %s : %w", path, err)
	}

	fields := strings.Fields(string(content[:read]))
	if len(fields) < 2 {
		return "", fmt.Errorf("%s n'est pas une clé publique d'hôte", path)
	}
	return hostPattern(address, port) + " " + fields[0] + " " + fields[1] + "\n", nil
}

// hostPattern : ssh écrit l'hôte nu sur le port 22, et entre crochets sinon.
func hostPattern(address string, port int) string {
	if port == 22 {
		return address
	}
	return "[" + address + "]:" + strconv.Itoa(port)
}
