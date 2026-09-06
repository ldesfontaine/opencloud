package validate

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// DigestPrefix : le seul algorithme accepté pour désigner une image.
const DigestPrefix = "sha256:"

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// ErrInvalidDigest : la valeur ne désigne pas une image par son empreinte.
var ErrInvalidDigest = errors.New("invalid digest")

// Digest nomme le cas du tag à part : « latest » désigne une image qui change,
// une empreinte désigne des octets.
func Digest(value string) error {
	if !strings.HasPrefix(value, DigestPrefix) {
		return fmt.Errorf("%w: names a tag, never a digest", ErrInvalidDigest)
	}
	if !digestPattern.MatchString(value) {
		return fmt.Errorf("%w: expected %s followed by 64 lowercase hexadecimal characters",
			ErrInvalidDigest, DigestPrefix)
	}
	return nil
}
