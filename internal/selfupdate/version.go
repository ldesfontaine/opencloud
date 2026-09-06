package selfupdate

import (
	"cmp"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrNotAReleaseVersion : la chaîne n'est pas un X.Y.Z publié. Un build de
// développement (dev, 0.0.1-3-gabc-dirty) n'a pas de release à suivre.
var ErrNotAReleaseVersion = errors.New("not a release version")

// Version est un numéro semver X.Y.Z. Le tag GitHub le préfixe d'un v.
type Version struct {
	Major int
	Minor int
	Patch int
}

// ParseVersion accepte "0.1.2" et "v0.1.2", rien d'autre.
func ParseVersion(text string) (Version, error) {
	parts := strings.Split(strings.TrimPrefix(text, "v"), ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("%w: %q", ErrNotAReleaseVersion, text)
	}

	var numbers [3]int
	for index, part := range parts {
		number, err := parseVersionNumber(part)
		if err != nil {
			return Version{}, fmt.Errorf("%w: %q", ErrNotAReleaseVersion, text)
		}
		numbers[index] = number
	}
	return Version{Major: numbers[0], Minor: numbers[1], Patch: numbers[2]}, nil
}

// Un nombre sans signe ni zéro de tête : "01" n'est pas une version.
func parseVersionNumber(part string) (int, error) {
	if part == "" || (len(part) > 1 && part[0] == '0') {
		return 0, errors.New("malformed number")
	}
	for _, character := range part {
		if character < '0' || character > '9' {
			return 0, errors.New("malformed number")
		}
	}
	return strconv.Atoi(part)
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// Tag est le nom du tag git et de la release GitHub.
func (v Version) Tag() string {
	return "v" + v.String()
}

// Compare rend -1, 0 ou 1 selon que v précède, égale ou suit other.
func (v Version) Compare(other Version) int {
	if v.Major != other.Major {
		return cmp.Compare(v.Major, other.Major)
	}
	if v.Minor != other.Minor {
		return cmp.Compare(v.Minor, other.Minor)
	}
	return cmp.Compare(v.Patch, other.Patch)
}

// IsSequentialUpgradeFrom dit si passer de current à v ne saute aucune
// version mineure : un correctif de la même série, la série suivante, ou la
// majeure suivante en X.0. Chaque version sait migrer depuis la précédente,
// pas depuis n'importe laquelle (Headscale).
func (v Version) IsSequentialUpgradeFrom(current Version) bool {
	if v.Compare(current) <= 0 {
		return false
	}
	if v.Major == current.Major {
		return v.Minor == current.Minor || v.Minor == current.Minor+1
	}
	return v.Major == current.Major+1 && v.Minor == 0
}
