package validate

import (
	"errors"
	"strings"
	"testing"
)

func TestSlug_AcceptsServiceAndEnvironmentNames(t *testing.T) {
	cases := map[string]string{
		"une lettre":        "a",
		"nom courant":       "nextcloud",
		"tiret":             "mon-service",
		"chiffres":          "app2",
		"longueur maximale": "a" + strings.Repeat("b", MaxSlugLength-1),
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			if err := Slug(value); err != nil {
				t.Fatalf("%q doit passer : %v", value, err)
			}
		})
	}
}

func TestSlug_RefusesEverythingElse(t *testing.T) {
	cases := map[string]string{
		"vide":            "",
		"trop long":       "a" + strings.Repeat("b", MaxSlugLength),
		"chiffre en tête": "2app",
		"tiret en tête":   "-app",
		"majuscule":       "App",
		"point":           "app.prod",
		"souligné":        "app_prod",
		"espace":          "mon service",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			if err := Slug(value); !errors.Is(err, ErrInvalidSlug) {
				t.Fatalf("%q doit être refusé, reçu %v", value, err)
			}
		})
	}
}
