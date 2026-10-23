package backup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"filippo.io/age"
)

const (
	fingerprintLength = 16
	// Une clé privée age tient en 74 caractères : au-delà, ce n'en est pas une.
	maxIdentityBytes = 1024
)

// ErrWrongPassphrase : la phrase n'ouvre pas la clé scellée.
var ErrWrongPassphrase = errors.New("the passphrase does not open the key")

// NormalizePassphrase retire les espaces de bord : la création et
// l'ouverture lisent la même phrase.
func NormalizePassphrase(passphrase string) string {
	return strings.TrimSpace(passphrase)
}

func checkPassphrase(passphrase string) error {
	length := utf8.RuneCountInString(passphrase)
	if length < MinPassphrase || length > MaxPassphrase {
		return ErrPassphraseTooShort
	}
	return nil
}

// newKeyring tire la paire : la publique s'écrit en clair, la privée est
// scellée par la phrase (age, scrypt) et la phrase n'est gardée nulle part.
// workFactor est le coût de scrypt en puissance de deux ; zéro prend celui
// d'age, qui demande un quart de gigaoctet le temps d'une création.
func newKeyring(passphrase string, workFactor int, now time.Time) (Keyring, error) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		return Keyring{}, fmt.Errorf("generate backup key: %w", err)
	}
	locker, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return Keyring{}, fmt.Errorf("seal backup key: %w", err)
	}
	if workFactor > 0 {
		locker.SetWorkFactor(workFactor)
	}
	var sealed bytes.Buffer
	writer, err := age.Encrypt(&sealed, locker)
	if err != nil {
		return Keyring{}, fmt.Errorf("seal backup key: %w", err)
	}
	if _, err := io.WriteString(writer, identity.String()+"\n"); err != nil {
		return Keyring{}, fmt.Errorf("seal backup key: %w", err)
	}
	if err := writer.Close(); err != nil {
		return Keyring{}, fmt.Errorf("seal backup key: %w", err)
	}
	recipient := identity.Recipient().String()
	return Keyring{
		Recipient: recipient, Fingerprint: fingerprintOf(recipient),
		IdentitySealed: sealed.Bytes(), CreatedAt: now,
	}, nil
}

// fingerprintOf est le début du SHA-256 de la clé publique : de quoi
// reconnaître la clé d'une sauvegarde sans la lire.
func fingerprintOf(recipient string) string {
	sum := sha256.Sum256([]byte(recipient))
	return hex.EncodeToString(sum[:])[:fingerprintLength]
}

// OpenIdentity ouvre la clé privée scellée avec la phrase : ce que fait la
// restauration, hors ligne. Le serveur ne s'en sert que dans ses tests.
func OpenIdentity(sealed []byte, passphrase string) (*age.X25519Identity, error) {
	unlocker, err := age.NewScryptIdentity(NormalizePassphrase(passphrase))
	if err != nil {
		return nil, fmt.Errorf("open backup key: %w", err)
	}
	reader, err := age.Decrypt(bytes.NewReader(sealed), unlocker)
	if err != nil {
		var wrong *age.NoIdentityMatchError
		if errors.As(err, &wrong) {
			return nil, ErrWrongPassphrase
		}
		return nil, fmt.Errorf("open backup key: %w", err)
	}
	content, err := io.ReadAll(io.LimitReader(reader, maxIdentityBytes))
	if err != nil {
		return nil, fmt.Errorf("open backup key: %w", err)
	}
	identity, err := age.ParseX25519Identity(strings.TrimSpace(string(content)))
	if err != nil {
		return nil, fmt.Errorf("open backup key: %w", err)
	}
	return identity, nil
}
