package machine

import (
	"github.com/ldesfontaine/opencloud/internal/anchor"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// ErrAnchorInvalid : une ancre demandée à l'installation n'est pas de la forme
// de son type, ou dépasse le nombre de valeurs que le type porte.
var ErrAnchorInvalid = refusal.Invalid("machine.anchor_invalid", "trust anchors must be known kinds, each with valid, distinct values within its limit")

// ValidateAnchors juge les ancres qu'une ligne d'installation va poser : des
// types du registre, des listes valables. Aucune ancre est valable.
func ValidateAnchors(anchors map[string][]string) error {
	for name, values := range anchors {
		kind, known := anchor.Lookup(name)
		if !known || (len(values) > 0 && !kind.ValidList(values)) {
			return ErrAnchorInvalid
		}
	}
	return nil
}
