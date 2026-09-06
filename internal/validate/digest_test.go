package validate

import (
	"errors"
	"strings"
	"testing"
)

const exampleDigest = DigestPrefix + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestDigest_AcceptsASha256Digest(t *testing.T) {
	if err := Digest(exampleDigest); err != nil {
		t.Fatalf("%q doit passer : %v", exampleDigest, err)
	}
}

func TestDigest_RefusesATagAndSaysSo(t *testing.T) {
	err := Digest("latest")
	if !errors.Is(err, ErrInvalidDigest) {
		t.Fatalf("un tag doit être refusé, reçu %v", err)
	}
	if !strings.Contains(err.Error(), "tag") {
		t.Fatalf("le refus doit nommer le tag : %v", err)
	}
}

func TestDigest_RefusesMalformedDigests(t *testing.T) {
	cases := map[string]string{
		"vide":             "",
		"sans préfixe":     strings.TrimPrefix(exampleDigest, DigestPrefix),
		"autre algorithme": "sha512:" + strings.TrimPrefix(exampleDigest, DigestPrefix),
		"trop court":       exampleDigest[:len(exampleDigest)-1],
		"trop long":        exampleDigest + "0",
		"majuscules":       strings.ToUpper(exampleDigest),
		"hors hexadécimal": DigestPrefix + strings.Repeat("g", 64),
		"image et tag":     "nginx:1.27",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			if err := Digest(value); !errors.Is(err, ErrInvalidDigest) {
				t.Fatalf("%q doit être refusé, reçu %v", value, err)
			}
		})
	}
}
