package validate

import (
	"errors"
	"fmt"
	"regexp"
)

// MaxAccountLength : la borne de useradd sur les distributions visées.
const MaxAccountLength = 32

// La forme d'un compte système : minuscules, chiffres, souligné et tiret,
// jamais un tiret ni un chiffre en tête.
var accountPattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// ErrInvalidAccount : le nom de compte système est hors forme.
var ErrInvalidAccount = errors.New("invalid account")

func Account(value string) error {
	if !accountPattern.MatchString(value) {
		return fmt.Errorf("%w: expected a lowercase letter or '_' then up to %d lowercase letters, digits, '_' or '-'",
			ErrInvalidAccount, MaxAccountLength-1)
	}
	return nil
}
