package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// ErrChecksumMissing : SHA256SUMS ne mentionne pas le fichier.
var ErrChecksumMissing = errors.New("checksum missing")

// findChecksum lit la somme d'un fichier dans un SHA256SUMS, au format de
// sha256sum : « hex  nom » par ligne, le nom parfois précédé d'une étoile.
func findChecksum(sums []byte, name string) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != name {
			continue
		}

		decoded, err := hex.DecodeString(fields[0])
		if err != nil || len(decoded) != sha256.Size {
			return digest, fmt.Errorf("malformed checksum for %s: %q", name, fields[0])
		}
		copy(digest[:], decoded)
		return digest, nil
	}
	return digest, fmt.Errorf("%w: %s", ErrChecksumMissing, name)
}
