package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Paramètres argon2id : le minimum recommandé par l'OWASP (2023). La machine
// openCloud est partagée, on ne lui prend pas 64 Mo par tentative.
const (
	argonMemoryKiB = 19 * 1024
	argonTime      = 2
	argonThreads   = 1
	argonSaltBytes = 16
	argonKeyBytes  = 32
)

// ErrMalformedHash : l'empreinte stockée n'a pas le format attendu.
var ErrMalformedHash = errors.New("malformed password hash")

// HashPassword rend l'empreinte au format PHC :
// $argon2id$v=19$m=…,t=…,p=…$<sel>$<clé>, sel et clé en base64 sans padding.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyBytes)

	encoding := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKiB, argonTime, argonThreads,
		encoding.EncodeToString(salt), encoding.EncodeToString(key)), nil
}

// VerifyPassword compare en temps constant. Les paramètres sont relus dans
// l'empreinte : on pourra les renforcer plus tard sans casser les comptes.
func VerifyPassword(password, encodedHash string) (bool, error) {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, ErrMalformedHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, ErrMalformedHash
	}

	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false, ErrMalformedHash
	}

	encoding := base64.RawStdEncoding
	salt, err := encoding.DecodeString(parts[4])
	if err != nil {
		return false, ErrMalformedHash
	}
	expectedKey, err := encoding.DecodeString(parts[5])
	if err != nil {
		return false, ErrMalformedHash
	}

	// gosec G115 : threads est déjà un uint8, la conversion est sûre.
	key := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(expectedKey))) // #nosec G115
	return subtle.ConstantTimeCompare(key, expectedKey) == 1, nil
}
