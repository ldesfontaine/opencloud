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
	// Derrière un mandataire qui authentifie (Authelia) et transmet
	// l'identité dans un en-tête ; cru seulement depuis trusted_proxies.
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
	// L'en-tête qu'Authelia pose, et que Traefik recopie, après
	// authentification.
	DefaultForwardHeader = "Remote-User"
	MaxHeaderLength      = 64
)

// State est l'état stocké. PasswordHash ne sort jamais du processus.
// Header n'a de sens qu'en mode forward : le nom de l'en-tête d'identité.
type State struct {
	Mode         Mode
	Username     string
	PasswordHash string
	Header       string
	EnabledAt    time.Time
}

var (
	ErrAlreadyEnabled   = errors.New("auth: already enabled")
	ErrDisabled         = errors.New("auth: disabled")
	ErrUsernameInvalid  = errors.New("auth: username invalid")
	ErrPasswordTooShort = errors.New("auth: password too short")
	ErrPasswordTooLong  = errors.New("auth: password too long")
	ErrHeaderInvalid    = errors.New("auth: header name invalid")
	// En mode forward, c'est le mandataire qui authentifie : il n'y a pas
	// de mot de passe à changer.
	ErrNoPassword = errors.New("auth: no password in this mode")
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

// ValidateHeader : un nom d'en-tête HTTP simple, lettres, chiffres et « - »,
// pour qu'aucune valeur saisie ne se lise autrement qu'un nom.
func ValidateHeader(header string) error {
	if len(header) == 0 || len(header) > MaxHeaderLength {
		return ErrHeaderInvalid
	}
	for _, character := range header {
		isLetter := (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z')
		isDigit := character >= '0' && character <= '9'
		if !isLetter && !isDigit && character != '-' {
			return ErrHeaderInvalid
		}
	}
	return nil
}
