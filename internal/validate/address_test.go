package validate

import (
	"errors"
	"testing"
)

func TestAddress_NormalizesWhatItAccepts(t *testing.T) {
	cases := map[string]string{
		"192.168.1.10": "192.168.1.10",
		"2001:0db8:0000:0000:0000:0000:0000:0001": "2001:db8::1",
		"2001:DB8::1": "2001:db8::1",
		"::1":         "::1",
	}
	for value, expected := range cases {
		t.Run(value, func(t *testing.T) {
			normalized, err := Address(value)
			if err != nil {
				t.Fatalf("%q doit passer : %v", value, err)
			}
			if normalized != expected {
				t.Fatalf("Address(%q) = %q, attendu %q", value, normalized, expected)
			}
		})
	}
}

func TestAddress_RefusesEverythingThatIsNotAnAddress(t *testing.T) {
	cases := map[string]string{
		"vide":             "",
		"nom d'hôte":       "exemple.com",
		"avec le port":     "192.168.1.10:22",
		"sous-réseau":      "192.168.1.0/24",
		"octet hors borne": "192.168.1.256",
		"trois octets":     "192.168.1",
		"espace":           " 192.168.1.10",
		"zéros non permis": "192.168.001.010",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Address(value); !errors.Is(err, ErrInvalidAddress) {
				t.Fatalf("%q doit être refusé, reçu %v", value, err)
			}
		})
	}
}
