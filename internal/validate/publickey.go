package validate

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Une clé publique au format authorized_keys : le type, le blob en base64, et
// un commentaire libre que personne ne lit sauf un humain.
const (
	PublicKeyType = "ssh-ed25519"

	// Le blob d'une clé ed25519 : deux chaînes SSH, « ssh-ed25519 » puis les
	// 32 octets de la clé, chacune précédée de sa longueur sur 4 octets.
	publicKeyBlobLength = 4 + len(PublicKeyType) + 4 + ed25519KeyLength
	ed25519KeyLength    = 32

	// MaxPublicKeyCommentLength : de quoi nommer une machine, pas d'y loger
	// une charge utile.
	MaxPublicKeyCommentLength = 128
)

// ErrInvalidPublicKey : la valeur n'est pas une clé ssh-ed25519 lisible.
var ErrInvalidPublicKey = errors.New("invalid public key")

// PublicKey n'accepte qu'ed25519 : une seule courbe à éprouver, et c'est celle
// que l'enrôlement pose.
func PublicKey(value string) error {
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("%w: expected a single line", ErrInvalidPublicKey)
	}
	fields := strings.Fields(value)
	if len(fields) < 2 || len(fields) > 3 {
		return fmt.Errorf("%w: expected '%s <base64> [comment]'", ErrInvalidPublicKey, PublicKeyType)
	}
	if fields[0] != PublicKeyType {
		return fmt.Errorf("%w: expected type %s", ErrInvalidPublicKey, PublicKeyType)
	}
	if err := checkEd25519Blob(fields[1]); err != nil {
		return err
	}
	if len(fields) == 3 {
		return checkPublicKeyComment(fields[2])
	}
	return nil
}

func checkEd25519Blob(encoded string) error {
	blob, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("%w: expected standard base64", ErrInvalidPublicKey)
	}
	if len(blob) != publicKeyBlobLength {
		return fmt.Errorf("%w: expected a %d byte blob, got %d", ErrInvalidPublicKey, publicKeyBlobLength, len(blob))
	}
	// Le blob se relit comme SSH l'écrit : le type doit s'y répéter, sinon la
	// base64 dit une chose et le contenu une autre.
	typeLength := binary.BigEndian.Uint32(blob[0:4])
	if int(typeLength) != len(PublicKeyType) || string(blob[4:4+typeLength]) != PublicKeyType {
		return fmt.Errorf("%w: blob does not name %s", ErrInvalidPublicKey, PublicKeyType)
	}
	keyStart := 4 + int(typeLength)
	keyLength := binary.BigEndian.Uint32(blob[keyStart : keyStart+4])
	if int(keyLength) != ed25519KeyLength {
		return fmt.Errorf("%w: expected a %d byte key", ErrInvalidPublicKey, ed25519KeyLength)
	}
	return nil
}

func checkPublicKeyComment(comment string) error {
	if len(comment) > MaxPublicKeyCommentLength {
		return fmt.Errorf("%w: comment longer than %d characters", ErrInvalidPublicKey, MaxPublicKeyCommentLength)
	}
	if !utf8.ValidString(comment) {
		return fmt.Errorf("%w: comment is not valid UTF-8", ErrInvalidPublicKey)
	}
	for _, character := range comment {
		if !unicode.IsPrint(character) {
			return fmt.Errorf("%w: comment holds a control character", ErrInvalidPublicKey)
		}
	}
	return nil
}
