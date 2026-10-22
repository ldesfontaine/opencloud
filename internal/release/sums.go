package release

import (
	"errors"
	"fmt"
	"strings"
)

const sha256HexSize = 64

// ParseSums lit un fichier SHA256SUMS : une ligne par fichier, « somme en
// 64 hexadécimaux minuscules, deux espaces (ou un espace et une étoile), nom ».
// Le nom est un nom de fichier seul, une fois. Rend somme par nom ; tout autre
// écart est refusé, y compris un fichier vide.
func ParseSums(content []byte) (map[string]string, error) {
	lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	sums := make(map[string]string, len(lines))
	for number, line := range lines {
		digest, name, err := parseSumsLine(line)
		if err != nil {
			return nil, fmt.Errorf("%w: sums line %d: %v", ErrMalformed, number+1, err)
		}
		if _, seen := sums[name]; seen {
			return nil, fmt.Errorf("%w: sums line %d: %s listed twice", ErrMalformed, number+1, name)
		}
		sums[name] = digest
	}
	return sums, nil
}

func parseSumsLine(line string) (digest, name string, err error) {
	if len(line) < sha256HexSize+3 {
		return "", "", errors.New("too short")
	}
	digest = line[:sha256HexSize]
	for _, char := range digest {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return "", "", errors.New("digest is not 64 lowercase hexadecimal digits")
		}
	}
	separator := line[sha256HexSize : sha256HexSize+2]
	if separator != "  " && separator != " *" {
		return "", "", errors.New("digest and name not separated by two spaces")
	}
	name = line[sha256HexSize+2:]
	if strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return "", "", errors.New("name is not a plain file name")
	}
	return digest, name, nil
}
