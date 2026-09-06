package validate

import (
	"errors"
	"strings"
	"testing"
)

func TestUsername_AcceptsPlainNames(t *testing.T) {
	for _, value := range []string{"admin", "lucas.d", "ops-2", "a", "x_y"} {
		if err := Username(value); err != nil {
			t.Fatalf("%q doit passer : %v", value, err)
		}
	}
}

func TestUsername_RefusesEmptyLongOrOddNames(t *testing.T) {
	cases := map[string]string{
		"vide":               "",
		"trop long":          strings.Repeat("a", MaxUsernameLength+1),
		"majuscule":          "Admin",
		"espace":             "ad min",
		"tiret en tête":      "-admin",
		"caractère exotique": "admin\x00",
		"accent":             "élise",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			if err := Username(value); !errors.Is(err, ErrInvalidUsername) {
				t.Fatalf("%q doit être refusé, reçu %v", value, err)
			}
		})
	}
}
