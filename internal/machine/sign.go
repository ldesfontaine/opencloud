package machine

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	NonceSize = 32
	idSize    = 16
	// nonce(32) || id(16) || horodatage big-endian(8).
	signedPayloadSize = NonceSize + idSize + 8
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

func NewNonce() ([]byte, error) {
	nonce := make([]byte, NonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return nonce, nil
}

// SignedPayload est l'octet-à-octet que l'agent signe et que le serveur
// vérifie. Les deux côtés passent par cette fonction, jamais par une copie.
func SignedPayload(nonce []byte, machineID string, timestamp int64) ([]byte, error) {
	if len(nonce) != NonceSize {
		return nil, ErrBadNonce
	}
	id, err := idBytes(machineID)
	if err != nil {
		return nil, err
	}
	payload := make([]byte, 0, signedPayloadSize)
	payload = append(payload, nonce...)
	payload = append(payload, id...)
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
