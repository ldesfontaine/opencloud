package release

import (
	"cmp"
	"fmt"
	"regexp"
	"strconv"
)

// Version est vX.Y.Z, sans zéro de tête ni suffixe : la forme des tags qui
// déclenchent une publication.
type Version struct {
	Major, Minor, Patch int
}

var versionPattern = regexp.MustCompile(`^v(0|[1-9][0-9]{0,4})\.(0|[1-9][0-9]{0,4})\.(0|[1-9][0-9]{0,4})$`)

// ParseVersion lit « v0.0.2 » ; tout autre texte est refusé.
func ParseVersion(text string) (Version, error) {
	match := versionPattern.FindStringSubmatch(text)
	if match == nil {
		return Version{}, fmt.Errorf("%w: version %q", ErrMalformed, text)
	}
	// Les trois nombres tiennent en cinq chiffres : Atoi ne peut pas échouer.
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	patch, _ := strconv.Atoi(match[3])
	return Version{Major: major, Minor: minor, Patch: patch}, nil
}

func (version Version) String() string {
	return fmt.Sprintf("v%d.%d.%d", version.Major, version.Minor, version.Patch)
}

// Compare rend -1, 0 ou 1 : la version la plus récente est la plus grande.
func (version Version) Compare(other Version) int {
	return cmp.Or(
		cmp.Compare(version.Major, other.Major),
		cmp.Compare(version.Minor, other.Minor),
		cmp.Compare(version.Patch, other.Patch),
	)
}
