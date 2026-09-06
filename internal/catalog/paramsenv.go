package catalog

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Le préfixe de toute variable d'action : ce que l'opérateur a saisi se
// distingue de ce que systemd pose lui-même.
const paramEnvPrefix = "OC_"

// ErrUnwritableValue : la valeur ne peut pas s'écrire dans un fichier que
// systemd relira à l'identique. Une faute de code : les types validés ne
// laissent pas passer ça.
var ErrUnwritableValue = errors.New("value cannot be written to an environment file")

func renderParamsEnv(specs []ParamSpec, values map[string]string) ([]byte, error) {
	var rendered strings.Builder
	for _, spec := range specs {
		value, given := values[spec.Name]
		if !given {
			continue
		}
		quoted, err := quoteEnvValue(value)
		if err != nil {
			return nil, fmt.Errorf("render %s: %w", spec.Name, err)
		}
		rendered.WriteString(paramEnvPrefix + strings.ToUpper(spec.Name) + "=" + quoted + "\n")
	}
	return []byte(rendered.String()), nil
}

// quoteEnvValue écrit une valeur pour EnvironmentFile= de systemd, d'après
// man systemd.exec. Les guillemets doubles sont systématiques : sans eux, les
// blancs de tête et de queue sont jetés, et « TITLE=Your Cloud » se relit
// « Your » — le piège d'origine, chez your-cloud. Entre ces guillemets, un
// antislash protège le caractère qui suit parmi " \ ` $ : ce sont donc les
// quatre à échapper, et aucun autre.
//
// Les types du §4 ont des alphabets étroits ; le quoting est juste pour
// n'importe quel octet quand même — c'est la seconde ligne de défense.
func quoteEnvValue(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("%w: not valid UTF-8", ErrUnwritableValue)
	}

	var quoted strings.Builder
	quoted.WriteByte('"')
	for _, character := range value {
		switch character {
		case '\n', '\r':
			// Un antislash devant un saut de ligne continue la ligne, et un
			// saut nu la termine : dans les deux cas systemd ne relit pas ce
			// qu'on a écrit.
			return "", fmt.Errorf("%w: holds a line break", ErrUnwritableValue)
		case 0, '\uFEFF':
			// NUL et la marque d'ordre des octets : systemd les nomme hors du
			// fichier, quelle que soit la citation.
			return "", fmt.Errorf("%w: holds a character systemd refuses", ErrUnwritableValue)
		case '"', '\\', '`', '$':
			quoted.WriteByte('\\')
			quoted.WriteRune(character)
		default:
			quoted.WriteRune(character)
		}
	}
	quoted.WriteByte('"')
	return quoted.String(), nil
}
