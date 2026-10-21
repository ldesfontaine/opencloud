package machine

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/brand"
)

const (
	// La taille de l'aléa que l'agent tire pour chaque preuve.
	NonceSize = 32
	idSize    = 16
	// Un identifiant de machine tient en un UUID ou « local » : au-delà, ce
	// n'est pas un agent qui parle.
	maxProofIDBytes = 64
)

// NewID tire un UUID v4 ; c'est l'agent qui le fait, l'id est à lui.
func NewID() (string, error) {
	raw := make([]byte, idSize)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:]), nil
}

// NewNonce tire l'aléa d'une preuve. crypto/rand ne rend plus d'erreur
// depuis Go 1.24.
func NewNonce() []byte {
	nonce := make([]byte, NonceSize)
	_, _ = rand.Read(nonce)
	return nonce
}

// SignedPayload est l'octet-à-octet que l'agent signe pour ouvrir son flux
// (protocole 2) et que le serveur vérifie : l'étiquette de domaine, l'aléa
// tiré par l'agent, l'identifiant de la machine précédé de sa longueur,
// l'horodatage big-endian. Les deux côtés passent par cette fonction, jamais
// par une copie. L'identifiant est pris tel quel : la machine openCloud
// (« local ») signe comme les autres.
func SignedPayload(nonce []byte, machineID string, timestamp int64) ([]byte, error) {
	if len(nonce) != NonceSize {
		return nil, ErrBadProof
	}
	if machineID == "" || len(machineID) > maxProofIDBytes {
		return nil, ErrBadID
	}
	payload := make([]byte, 0, len(brand.AgentProofDomain)+NonceSize+4+len(machineID)+8)
	payload = append(payload, brand.AgentProofDomain...)
	payload = append(payload, nonce...)
	payload = binary.BigEndian.AppendUint32(payload, uint32(len(machineID))) // #nosec G115 -- borné juste au-dessus.
	payload = append(payload, machineID...)
	payload = binary.BigEndian.AppendUint64(payload, uint64(timestamp)) // #nosec G115 -- un horodatage Unix ne dépasse pas int64.
	return payload, nil
}

func Sign(privateKey ed25519.PrivateKey, payload []byte) []byte {
	return ed25519.Sign(privateKey, payload)
}

// Verify refuse une clé de la mauvaise taille avant ed25519.Verify, qui
// paniquerait dessus.
func Verify(publicKey []byte, payload, signature []byte) bool {
	if len(publicKey) != ed25519.PublicKeySize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(publicKey), payload, signature)
}

func idBytes(machineID string) ([]byte, error) {
	clean := strings.ReplaceAll(machineID, "-", "")
	if len(clean) != idSize*2 {
		return nil, ErrBadID
	}
	raw, err := hex.DecodeString(clean)
	if err != nil {
		return nil, ErrBadID
	}
	return raw, nil
}

func ValidateID(machineID string) error {
	_, err := idBytes(machineID)
	return err
}
