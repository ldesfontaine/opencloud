package credential

import "testing"

func TestMask(t *testing.T) {
	cases := map[string]string{
		"AbCdEfGhIjKl":        "AbCd…",
		"lecteur@pve!lecture": "lect…",
		"éàüöxyz":             "éàüö…",
		"abcd":                "…",
		"":                    "…",
	}
	for in, want := range cases {
		if got := Mask(in); got != want {
			t.Errorf("%q : got %q, want %q", in, got, want)
		}
	}
}
