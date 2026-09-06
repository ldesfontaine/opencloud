package selfupdate

import (
	"errors"
	"strings"
	"testing"
)

func TestFindChecksum_ReadsTheSha256sumFormat(t *testing.T) {
	sums := []byte(strings.Repeat("ab", 32) + "  opencloud_0.0.2_amd64.deb\n" +
		strings.Repeat("cd", 32) + " *opencloud_0.0.2_linux_amd64\n")

	digest, err := findChecksum(sums, "opencloud_0.0.2_linux_amd64")
	if err != nil {
		t.Fatal(err)
	}
	if digest[0] != 0xcd || digest[31] != 0xcd {
		t.Fatalf("somme lue = %x", digest)
	}
}

func TestFindChecksum_MissingOrMalformed_IsAnError(t *testing.T) {
	if _, err := findChecksum([]byte("abcd  other\n"), "opencloud"); !errors.Is(err, ErrChecksumMissing) {
		t.Fatalf("attendu ErrChecksumMissing, reçu %v", err)
	}
	if _, err := findChecksum([]byte("zz  opencloud\n"), "opencloud"); err == nil || errors.Is(err, ErrChecksumMissing) {
		t.Fatalf("une somme illisible est une autre erreur, reçu %v", err)
	}
}
