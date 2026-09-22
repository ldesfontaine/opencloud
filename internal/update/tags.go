package update

import "strings"

// Kind est ce qu'une mise à jour change : un composant de la version, ou
// seulement l'image derrière le même tag.
type Kind string

const (
	KindMajor  Kind = "major"
	KindMinor  Kind = "minor"
	KindPatch  Kind = "patch"
	KindDigest Kind = "digest"
)

// Les suffixes de variante qu'un tag porte après sa version : l'OS de
// base, pas une préversion. Du plus long au plus court, pour que
// « -slim-bookworm » passe avant « -bookworm ». Liste fermée : un
// suffixe inconnu fait du tag un canal suivi au digest.
var variants = []string{
	"-slim-trixie", "-slim-bookworm", "-slim-bullseye", "-slim-buster",
	"-alpine3.22", "-alpine3.21", "-alpine3.20", "-alpine3.19", "-alpine3.18",
	"-alpine", "-trixie", "-bookworm", "-bullseye", "-buster",
	"-noble", "-jammy", "-focal", "-slim",
}

// tagVersion est un tag lu comme une version : ses composants, sa
// variante, et s'il s'écrit avec un « v » devant.
type tagVersion struct {
	numbers []int
	variant string
	vPrefix bool
}

// parseTag lit « v1.2.3-alpine » ; « latest », « lts », « 1.2-rc1 » ne
// sont pas des versions.
func parseTag(tag string) (tagVersion, bool) {
	version, variant := splitVariant(tag)
	vPrefix := strings.HasPrefix(version, "v")
	version = strings.TrimPrefix(version, "v")
	if version == "" {
		return tagVersion{}, false
	}
	parts := strings.Split(version, ".")
	numbers := make([]int, 0, len(parts))
	for _, part := range parts {
		number, ok := parseNumber(part)
		if !ok {
			return tagVersion{}, false
		}
		numbers = append(numbers, number)
	}
	return tagVersion{numbers: numbers, variant: variant, vPrefix: vPrefix}, true
}

func splitVariant(tag string) (string, string) {
	lower := strings.ToLower(tag)
	for _, suffix := range variants {
		if strings.HasSuffix(lower, suffix) {
			return tag[:len(tag)-len(suffix)], suffix
		}
	}
	return tag, ""
}

// parseNumber lit un composant : des chiffres, neuf au plus, sinon c'est
// un identifiant de build ou un horodatage, pas une version.
func parseNumber(part string) (int, bool) {
	if part == "" || len(part) > 9 {
		return 0, false
	}
	number := 0
	for _, character := range part {
		if character < '0' || character > '9' {
			return 0, false
		}
		number = number*10 + int(character-'0')
	}
	return number, true
}

// Newer rend le tag le plus haut strictement plus récent que le courant
// et écrit de la même façon : même variante, même « v », même nombre de
// composants. Un tag à un ou deux composants est un canal : « 16 » suit
// la ligne 16 au digest, et « 17 » est sa majeure. Vide quand le tag
// courant n'est pas une version, ou que rien de plus récent n'existe.
func Newer(current string, tags []string) string {
	reference, ok := parseTag(current)
	if !ok {
		return ""
	}
	best := reference
	bestTag := ""
	for _, tag := range tags {
		candidate, ok := parseTag(tag)
		if !ok || !sameShape(candidate, reference) {
			continue
		}
		if compare(candidate.numbers, best.numbers) > 0 {
			best, bestTag = candidate, tag
		}
	}
	return bestTag
}

// sameShape : même variante, même « v », même précision ; et un majeur
// qui ne saute pas d'un ordre de grandeur, ce qui trahit un identifiant
// de build.
func sameShape(candidate, reference tagVersion) bool {
	if !strings.EqualFold(candidate.variant, reference.variant) || candidate.vPrefix != reference.vPrefix {
		return false
	}
	if len(candidate.numbers) != len(reference.numbers) {
		return false
	}
	return digits(candidate.numbers[0]) <= digits(reference.numbers[0])+1
}

func digits(number int) int {
	count := 1
	for number >= 10 {
		number /= 10
		count++
	}
	return count
}

func compare(a, b []int) int {
	for index := range a {
		if index >= len(b) {
			return 1
		}
		if a[index] != b[index] {
			if a[index] > b[index] {
				return 1
			}
			return -1
		}
	}
	if len(a) < len(b) {
		return -1
	}
	return 0
}

// Classify dit ce que le passage d'un tag à l'autre change : le premier
// composant qui diffère. Vide si les deux ne se comparent pas.
func Classify(current, newer string) Kind {
	from, ok := parseTag(current)
	if !ok {
		return ""
	}
	to, ok := parseTag(newer)
	if !ok || len(to.numbers) != len(from.numbers) {
		return ""
	}
	for index := range from.numbers {
		if to.numbers[index] == from.numbers[index] {
			continue
		}
		if to.numbers[index] < from.numbers[index] {
			return ""
		}
		switch index {
		case 0:
			return KindMajor
		case 1:
			return KindMinor
		default:
			return KindPatch
		}
	}
	return ""
}
