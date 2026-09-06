package validate

import (
	"errors"
	"fmt"
	"regexp"
)

// MaxSlugLength borne un nom de service ou d'environnement : il devient un
// segment de chemin sous /srv, pas une charge utile.
const MaxSlugLength = 32

var slugPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// ErrInvalidSlug : le nom de service ou d'environnement est hors forme.
var ErrInvalidSlug = errors.New("invalid slug")

func Slug(value string) error {
	if !slugPattern.MatchString(value) {
		return fmt.Errorf("%w: expected a lowercase letter then up to %d lowercase letters, digits or '-'",
			ErrInvalidSlug, MaxSlugLength-1)
	}
	return nil
}
