package validate

import (
	"errors"
	"fmt"
	"regexp"
)

// MaxUsernameLength borne un identifiant de compte : assez pour un nom
// d'opérateur, trop peu pour une charge utile.
const MaxUsernameLength = 64

// Minuscules, chiffres, point, tiret et souligné ; commence par une lettre ou
// un chiffre. Un identifiant se tape, il ne se colle pas.
var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// ErrInvalidUsername : l'identifiant ne respecte pas la forme attendue.
var ErrInvalidUsername = errors.New("invalid username")

// Username refuse tout identifiant hors forme avant qu'il ne coûte quoi que
// ce soit : ni requête, ni empreinte, ni place dans le frein.
func Username(value string) error {
	if value == "" || len(value) > MaxUsernameLength {
		return fmt.Errorf("%w: expected 1 to %d characters", ErrInvalidUsername, MaxUsernameLength)
	}
	if !usernamePattern.MatchString(value) {
		return fmt.Errorf("%w: expected lowercase letters, digits, '.', '_' or '-'", ErrInvalidUsername)
	}
	return nil
}
