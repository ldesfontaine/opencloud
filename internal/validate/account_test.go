package validate

import (
	"errors"
	"strings"
	"testing"
)

func TestAccount_AcceptsSystemAccountNames(t *testing.T) {
	cases := map[string]string{
		"une lettre":        "a",
		"compte openCloud":  "opencloud",
		"souligné en tête":  "_build",
		"tiret et chiffres": "svc-web2",
		"longueur maximale": "a" + strings.Repeat("b", MaxAccountLength-1),
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			if err := Account(value); err != nil {
				t.Fatalf("%q doit passer : %v", value, err)
			}
		})
	}
}

func TestAccount_RefusesEverythingElse(t *testing.T) {
	cases := map[string]string{
		"vide":            "",
		"trop long":       "a" + strings.Repeat("b", MaxAccountLength),
		"chiffre en tête": "2svc",
		"tiret en tête":   "-svc",
		"majuscule":       "Root",
		"point":           "svc.web",
		"espace":          "svc web",
		"deux-points":     "svc:web",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			if err := Account(value); !errors.Is(err, ErrInvalidAccount) {
				t.Fatalf("%q doit être refusé, reçu %v", value, err)
			}
		})
	}
}
