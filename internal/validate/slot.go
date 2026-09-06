package validate

import (
	"errors"
	"fmt"
	"regexp"
)

// MaxSlotLength borne le nom d'un slot de sauvegarde : il devient un nom de
// fichier sous data/backups.
const MaxSlotLength = 32

// Le point est hors de la forme : « . » et « .. » ne peuvent donc pas nommer
// un slot, et aucun nom ne remonte d'un dossier.
var slotPattern = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// ErrInvalidSlot : le nom de slot est hors forme.
var ErrInvalidSlot = errors.New("invalid slot")

func Slot(value string) error {
	if !slotPattern.MatchString(value) {
		return fmt.Errorf("%w: expected 1 to %d lowercase letters, digits or '-'", ErrInvalidSlot, MaxSlotLength)
	}
	return nil
}
