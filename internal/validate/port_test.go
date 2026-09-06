package validate

import (
	"errors"
	"testing"
)

func TestPort_AcceptsTheUnprivilegedRange(t *testing.T) {
	cases := map[string]int{
		"1024":  MinPort,
		"8080":  8080,
		"65535": MaxPort,
	}
	for value, expected := range cases {
		t.Run(value, func(t *testing.T) {
			number, err := Port(value)
			if err != nil {
				t.Fatalf("%q doit passer : %v", value, err)
			}
			if number != expected {
				t.Fatalf("Port(%q) = %d, attendu %d", value, number, expected)
			}
		})
	}
}

func TestPort_RefusesPrivilegedOutOfRangeAndNonDigits(t *testing.T) {
	cases := map[string]string{
		"vide":           "",
		"privilégié":     "443",
		"sous la borne":  "1023",
		"au-dessus":      "65536",
		"six chiffres":   "100000",
		"signe":          "+8080",
		"négatif":        "-1",
		"espace en tête": " 8080",
		"lettres":        "80a0",
		"hexadécimal":    "0x1f90",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Port(value); !errors.Is(err, ErrInvalidPort) {
				t.Fatalf("%q doit être refusé, reçu %v", value, err)
			}
		})
	}
}
