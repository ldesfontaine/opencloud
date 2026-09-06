package validate

import (
	"errors"
	"strings"
	"testing"
)

func TestSlot_AcceptsBackupSlotNames(t *testing.T) {
	cases := map[string]string{
		"un caractère":      "a",
		"chiffre en tête":   "2026-09-06",
		"nom de base":       "nextcloud-db",
		"longueur maximale": strings.Repeat("a", MaxSlotLength),
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			if err := Slot(value); err != nil {
				t.Fatalf("%q doit passer : %v", value, err)
			}
		})
	}
}

func TestSlot_RefusesDotsAndEverythingElse(t *testing.T) {
	cases := map[string]string{
		"vide":            "",
		"trop long":       strings.Repeat("a", MaxSlotLength+1),
		"point":           ".",
		"deux points":     "..",
		"remontée":        "../etc",
		"point au milieu": "sauvegarde.tar",
		"barre oblique":   "a/b",
		"majuscule":       "Slot",
		"souligné":        "slot_1",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			if err := Slot(value); !errors.Is(err, ErrInvalidSlot) {
				t.Fatalf("%q doit être refusé, reçu %v", value, err)
			}
		})
	}
}
