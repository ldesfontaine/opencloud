package validate

import (
	"errors"
	"fmt"
	"regexp"
)

// Bornes d'un nom de domaine : 3 à 253 octets, la limite de la résolution.
const (
	MinDomainLength = 3
	MaxDomainLength = 253
)

// Minuscules, chiffres, point et tiret, entre deux caractères alphanumériques :
// ni guillemet, ni antislash, ni espace n'entre dans cette forme.
var domainPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,251}[a-z0-9]$`)

// ErrInvalidDomain : le nom ne respecte pas la forme attendue.
var ErrInvalidDomain = errors.New("invalid domain")

func Domain(value string) error {
	if !domainPattern.MatchString(value) {
		return fmt.Errorf("%w: expected %d to %d lowercase letters, digits, '.' or '-'",
			ErrInvalidDomain, MinDomainLength, MaxDomainLength)
	}
	return nil
}
