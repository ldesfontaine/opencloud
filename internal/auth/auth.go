package auth

import (
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Mode dit comment l'interface est gardée.
type Mode string

const (
	// Personne n'est vérifié : qui atteint le port est l'opérateur.
	ModeNone Mode = "none"
	// HTTP Basic, identifiant et mot de passe choisis à l'activation.
	ModeBasic Mode = "basic"
	// Réservé : derrière un mandataire qui authentifie (Authelia) et
	// transmet l'identité. Rien ne l'active encore ; le serveur le refuse.
	ModeForward Mode = "forward"
)

func Modes() []Mode {
	return []Mode{ModeNone, ModeBasic, ModeForward}
}

const (
	MinPasswordLength = 12
	// bcrypt ignore tout ce qui dépasse 72 octets : on refuse plutôt que
	// de laisser croire qu'une fin de phrase compte.
	MaxPasswordBytes  = 72
	MaxUsernameLength = 64
)

// State est l'état stocké. PasswordHash ne sort jamais du processus.
type State struct {
	Mode         Mode
	Username     string
	PasswordHash string
	EnabledAt    time.Time
}

var (
	ErrAlreadyEnabled   = errors.New("auth: already enabled")
	ErrDisabled         = errors.New("auth: disabled")
	ErrUsernameInvalid  = errors.New("auth: username invalid")
	ErrPasswordTooShort = errors.New("auth: password too short")
	ErrPasswordTooLong  = errors.New("auth: password too long")
)

// ValidateUsername : un mot lisible, sans « : » que Basic prend pour le
// séparateur, ni caractère de contrôle ou espace en bord.
func ValidateUsername(username string) error {
	length := utf8.RuneCountInString(username)
	if length == 0 || length > MaxUsernameLength || !utf8.ValidString(username) {
		return ErrUsernameInvalid
	}
	if strings.TrimSpace(username) != username || strings.Contains(username, ":") {
		return ErrUsernameInvalid
	}
	for _, character := range username {
		if unicode.IsControl(character) {
			return ErrUsernameInvalid
		}
	}
	return nil
}

func ValidatePassword(password string) error {
	if utf8.RuneCountInString(password) < MinPasswordLength {
		return ErrPasswordTooShort
	}
	if len(password) > MaxPasswordBytes {
		return ErrPasswordTooLong
	}
	return nil
}
