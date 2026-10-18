package lang

import (
	"strconv"
	"strings"
)

// Les nombres que la langue écrit : le séparateur de milliers est celui de
// l'interface (« 1 204 » en français, « 1,204 » en anglais).
const narrowNoBreakSpace = " "

// FormatCount écrit un entier avec le séparateur de milliers de la langue.
func FormatCount(code Code, count int) string {
	digits := strconv.FormatInt(int64(count), 10)
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	separator := ","
	if code == French {
		separator = narrowNoBreakSpace
	}
	var grouped strings.Builder
	for index, digit := range digits {
		if index > 0 && (len(digits)-index)%3 == 0 {
			grouped.WriteString(separator)
		}
		grouped.WriteRune(digit)
	}
	return sign + grouped.String()
}

// isSingular dit si la langue met ce nombre au singulier : un en toute langue,
// et zéro en français.
func isSingular(code Code, count int) bool {
	return count == 1 || (code == French && count == 0)
}

// Counted écrit un texte qui compte quelque chose : la clé « key_one » quand
// le nombre est au singulier, « key » sinon. Le nombre, écrit avec le
// séparateur de milliers, est le premier argument du texte (%s) ; les autres
// suivent.
func (c Catalog) Counted(key string, count int, args ...any) string {
	if isSingular(c.code, count) {
		key += "_one"
	}
	return c.Format(key, append([]any{FormatCount(c.code, count)}, args...)...)
}
