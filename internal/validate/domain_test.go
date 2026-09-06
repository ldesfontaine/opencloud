package validate

import (
	"errors"
	"strings"
	"testing"
)

func TestDomain_AcceptsPlainNames(t *testing.T) {
	cases := map[string]string{
		"deux étiquettes":   "exemple.com",
		"sous-domaine":      "www.exemple.com",
		"tiret au milieu":   "mon-site.fr",
		"chiffres":          "1n2.fr",
		"longueur minimale": "a.b",
		"longueur maximale": strings.Repeat("a", MaxDomainLength),
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			if err := Domain(value); err != nil {
				t.Fatalf("%q doit passer : %v", value, err)
			}
		})
	}
}

func TestDomain_RefusesEverythingElse(t *testing.T) {
	cases := map[string]string{
		"vide":            "",
		"deux caractères": "ab",
		"trop long":       strings.Repeat("a", MaxDomainLength+1),
		"majuscule":       "Exemple.com",
		"espace":          "exemple .com",
		"guillemet":       "exemple\".com",
		"antislash":       "exemple\\.com",
		"point en tête":   ".exemple.com",
		"tiret en fin":    "exemple.com-",
		"souligné":        "exemple_1.com",
		"accent":          "exemplé.com",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			if err := Domain(value); !errors.Is(err, ErrInvalidDomain) {
				t.Fatalf("%q doit être refusé, reçu %v", value, err)
			}
		})
	}
}
