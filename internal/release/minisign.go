package release

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/blake2b"

	"github.com/ldesfontaine/opencloud/internal/brand"
)

var (
	// ErrMalformed : le texte n'a pas la forme d'une clé, d'une signature ou
	// d'un fichier de sommes.
	ErrMalformed = errors.New("release: malformed")
	// ErrLegacyAlgorithm : signature minisign sans pré-hachage ("Ed"), refusée.
	ErrLegacyAlgorithm = errors.New("release: legacy signature algorithm")
	// ErrWrongKey : la signature porte l'identifiant d'une autre clé.
	ErrWrongKey = errors.New("release: signature made by another key")
	// ErrBadSignature : le message ou le commentaire de confiance a changé.
	ErrBadSignature = errors.New("release: bad signature")
)

// Les marques du format minisign : l'algorithme de la clé publique, celui de
// la signature (pré-hachée par BLAKE2b-512), et les préfixes des commentaires.
const (
	publicAlgorithm    = "Ed"
	signatureAlgorithm = "ED"
	untrustedPrefix    = "untrusted comment: "
	trustedPrefix      = "trusted comment: "

	keyIDSize = 8
	// "Ed" ‖ identifiant ‖ clé, puis "ED" ‖ identifiant ‖ signature.
	publicKeyBytes = 2 + keyIDSize + ed25519.PublicKeySize
	signatureBytes = 2 + keyIDSize + ed25519.SignatureSize
	// La clé privée d'openCloud : identifiant ‖ graine. Pas le format
	// de minisign, qui chiffre la sienne par un mot de passe.
	privateKeyBytes = keyIDSize + ed25519.SeedSize
)

// PublicKey vérifie des signatures minisign.
type PublicKey struct {
	id  [keyIDSize]byte
	key ed25519.PublicKey
}

// PrivateKey signe au format minisign.
type PrivateKey struct {
	id  [keyIDSize]byte
	key ed25519.PrivateKey
}

// GenerateKey tire une paire neuve ; l'identifiant est aléatoire, comme
// celui de minisign.
func GenerateKey() (PublicKey, PrivateKey, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return PublicKey{}, PrivateKey{}, fmt.Errorf("generate key: %w", err)
	}
	var id [keyIDSize]byte
	if _, err := rand.Read(id[:]); err != nil {
		return PublicKey{}, PrivateKey{}, fmt.Errorf("generate key id: %w", err)
	}
	return PublicKey{id: id, key: publicKey}, PrivateKey{id: id, key: privateKey}, nil
}

// ParsePublicKey lit la ligne base64 d'une clé publique minisign, seule ou
// précédée de la ligne « untrusted comment: » d'un fichier .pub.
func ParsePublicKey(text string) (PublicKey, error) {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) == 2 {
		if !strings.HasPrefix(lines[0], untrustedPrefix) {
			return PublicKey{}, fmt.Errorf("%w: public key file without comment line", ErrMalformed)
		}
		lines = lines[1:]
	}
	if len(lines) != 1 {
		return PublicKey{}, fmt.Errorf("%w: public key has %d lines", ErrMalformed, len(lines))
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[0]))
	if err != nil || len(raw) != publicKeyBytes {
		return PublicKey{}, fmt.Errorf("%w: public key", ErrMalformed)
	}
	if string(raw[:2]) != publicAlgorithm {
		return PublicKey{}, fmt.Errorf("%w: public key algorithm", ErrMalformed)
	}
	key := PublicKey{key: ed25519.PublicKey(bytes.Clone(raw[2+keyIDSize:]))}
	copy(key.id[:], raw[2:2+keyIDSize])
	return key, nil
}

// String rend la ligne base64 de minisign, celle que `minisign -P` attend.
func (key PublicKey) String() string {
	raw := append([]byte(publicAlgorithm), key.id[:]...)
	raw = append(raw, key.key...)
	return base64.StdEncoding.EncodeToString(raw)
}

// ParsePrivateKey lit ce que PrivateKey.Encode écrit. Le message d'erreur ne
// reprend jamais le texte : il peut finir dans un journal.
func ParsePrivateKey(text string) (PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
	if err != nil || len(raw) != privateKeyBytes {
		return PrivateKey{}, fmt.Errorf("%w: private key", ErrMalformed)
	}
	key := PrivateKey{key: ed25519.NewKeyFromSeed(raw[keyIDSize:])}
	copy(key.id[:], raw[:keyIDSize])
	return key, nil
}

// Encode rend l'identifiant suivi de la graine, en base64 : c'est le
// contenu du secret du dépôt. Un nom explicite, pas String : la clé ne doit
// sortir que là où on l'écrit exprès.
func (key PrivateKey) Encode() string {
	if len(key.key) != ed25519.PrivateKeySize {
		return ""
	}
	raw := append(bytes.Clone(key.id[:]), key.key.Seed()...)
	return base64.StdEncoding.EncodeToString(raw)
}

// redactedPrivateKey est tout ce que fmt (et donc un journal) montre d'une
// clé privée, quel que soit le verbe : %v, %+v, %#v, %s, %x, %d.
const redactedPrivateKey = "release.PrivateKey(redacted)"

// Format masque la clé : sans lui, %v imprimerait les octets du champ.
func (key PrivateKey) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte(redactedPrivateKey))
}

// Sign rend le fichier .minisig de message : quatre lignes, `\n` final. Le
// commentaire de confiance est signé avec le message ; il tient sur une ligne.
func Sign(message []byte, key PrivateKey, trustedComment string) ([]byte, error) {
	if len(key.key) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: private key", ErrMalformed)
	}
	if strings.ContainsAny(trustedComment, "\r\n") {
		return nil, fmt.Errorf("%w: trusted comment on several lines", ErrMalformed)
	}
	digest := blake2b.Sum512(message)
	signature := ed25519.Sign(key.key, digest[:])
	globalSignature := ed25519.Sign(key.key, globalMessage(signature, trustedComment))

	header := append([]byte(signatureAlgorithm), key.id[:]...)
	header = append(header, signature...)
	var file strings.Builder
	file.WriteString(untrustedPrefix + brand.ReleaseSignatureComment + "\n")
	file.WriteString(base64.StdEncoding.EncodeToString(header) + "\n")
	file.WriteString(trustedPrefix + trustedComment + "\n")
	file.WriteString(base64.StdEncoding.EncodeToString(globalSignature) + "\n")
	return []byte(file.String()), nil
}

// Verify vérifie que signatureFile est la signature de message par key et
// rend le commentaire de confiance, signé avec lui. Seul l'algorithme
// pré-haché est accepté.
func Verify(message, signatureFile []byte, key PublicKey) (trustedComment string, err error) {
	if len(key.key) != ed25519.PublicKeySize {
		return "", fmt.Errorf("%w: public key", ErrMalformed)
	}
	lines, err := signatureLines(signatureFile)
	if err != nil {
		return "", err
	}
	header, err := decodeLine(lines[1], signatureBytes, "signature")
	if err != nil {
		return "", err
	}
	globalSignature, err := decodeLine(lines[3], ed25519.SignatureSize, "global signature")
	if err != nil {
		return "", err
	}
	switch string(header[:2]) {
	case signatureAlgorithm:
	case publicAlgorithm:
		return "", ErrLegacyAlgorithm
	default:
		return "", fmt.Errorf("%w: signature algorithm", ErrMalformed)
	}
	if !bytes.Equal(header[2:2+keyIDSize], key.id[:]) {
		return "", ErrWrongKey
	}
	signature := header[2+keyIDSize:]
	digest := blake2b.Sum512(message)
	if !ed25519.Verify(key.key, digest[:], signature) {
		return "", fmt.Errorf("%w: message", ErrBadSignature)
	}
	comment := strings.TrimPrefix(lines[2], trustedPrefix)
	if !ed25519.Verify(key.key, globalMessage(signature, comment), globalSignature) {
		return "", fmt.Errorf("%w: trusted comment", ErrBadSignature)
	}
	return comment, nil
}

// signatureLines coupe un .minisig en ses quatre lignes et contrôle les
// deux préfixes. Un `\r` est refusé : un fichier passé par un éditeur
// Windows n'est plus celui qui a été signé.
func signatureLines(file []byte) ([]string, error) {
	text := strings.TrimSuffix(string(file), "\n")
	if strings.Contains(text, "\r") {
		return nil, fmt.Errorf("%w: carriage return in signature file", ErrMalformed)
	}
	lines := strings.Split(text, "\n")
	if len(lines) != 4 {
		return nil, fmt.Errorf("%w: signature file has %d lines", ErrMalformed, len(lines))
	}
	if !strings.HasPrefix(lines[0], untrustedPrefix) || !strings.HasPrefix(lines[2], trustedPrefix) {
		return nil, fmt.Errorf("%w: comment prefix", ErrMalformed)
	}
	return lines, nil
}

func decodeLine(line string, size int, what string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(line)
	if err != nil || len(raw) != size {
		return nil, fmt.Errorf("%w: %s", ErrMalformed, what)
	}
	return raw, nil
}

// globalMessage est ce que signe la seconde signature : la première, suivie
// du commentaire de confiance sans son préfixe.
func globalMessage(signature []byte, trustedComment string) []byte {
	return append(bytes.Clone(signature), trustedComment...)
}
