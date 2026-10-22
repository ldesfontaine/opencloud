package auth

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// Mode dit comment l'interface est gardée.
type Mode string

const (
	// Aucun compte : l'installation attend sa première configuration, et
	// le serveur ne sert à l'opérateur que de quoi créer le compte.
	ModeNone Mode = "none"
	// HTTP Basic, identifiant et mot de passe choisis à l'activation.
	ModeBasic Mode = "basic"
	// Derrière un SSO en amont qui authentifie (Authelia) et transmet
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

// Same dit si deux états sont le même, champ pour champ. Le service s'en sert
// pour ne rien écrire sur un état que la requête n'a pas vu.
func (s State) Same(other State) bool {
	return s.Mode == other.Mode &&
		s.Username == other.Username &&
		s.PasswordHash == other.PasswordHash &&
		s.Header == other.Header &&
		s.EnabledAt.Equal(other.EnabledAt)
}

var (
	ErrAlreadyEnabled   = refusal.Conflict("auth.already_enabled", "auth: already enabled")
	ErrDisabled         = refusal.Conflict("auth.disabled", "auth: disabled")
	ErrUsernameInvalid  = refusal.Invalid("auth.username_invalid", "auth: username invalid")
	ErrPasswordTooShort = refusal.Invalid("auth.password_too_short", "auth: password too short")
	ErrPasswordTooLong  = refusal.Invalid("auth.password_too_long", "auth: password too long")
	ErrHeaderInvalid    = refusal.Invalid("auth.header_invalid", "auth: header name invalid")
	// En mode forward, c'est le SSO qui authentifie : il n'y a pas
	// de mot de passe à changer.
	ErrNoPassword = refusal.Conflict("auth.no_password", "auth: no password in this mode")
	// L'état a changé entre l'instant où la requête a été autorisée et celui
	// où elle aurait pris effet : elle ne s'applique pas.
	ErrStateChanged = refusal.Conflict("auth.state_changed", "auth: state changed")
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
