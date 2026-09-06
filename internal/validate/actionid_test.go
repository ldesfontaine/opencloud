package validate

import (
	"errors"
	"strings"
	"testing"
)

func TestActionID_AcceptsTheFormSudoersAlsoAccepts(t *testing.T) {
	for _, value := range []string{"a", "diagnostiquer-20260906-1", strings.Repeat("a", 40)} {
		if err := ActionID(value); err != nil {
			t.Fatalf("%q doit passer : %v", value, err)
		}
	}
}

func TestActionID_RefusesEverythingElse(t *testing.T) {
	cases := map[string]string{
		"vide":          "",
		"trop long":     strings.Repeat("a", 41),
		"majuscule":     "Action",
		"espace":        "a b",
		"remontée":      "../x",
		"barre oblique": "a/b",
		"point":         "a.b",
		"souligné":      "a_b",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			if err := ActionID(value); !errors.Is(err, ErrInvalidActionID) {
				t.Fatalf("%q doit être refusé, reçu %v", value, err)
			}
		})
	}
}
