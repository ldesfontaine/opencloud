package validate

import (
	"errors"
	"fmt"

	"github.com/ldesfontaine/opencloud/internal/actiondir"
)

// ErrInvalidActionID : l'identifiant d'action est hors forme.
var ErrInvalidActionID = errors.New("invalid action id")

// ActionID ne redéfinit pas la forme : elle vit dans actiondir, partagée par
// sudoers, le lanceur et le nom d'unité. Deux définitions divergeraient.
func ActionID(value string) error {
	if !actiondir.ValidID(value) {
		return fmt.Errorf("%w: expected 1 to 40 lowercase letters, digits or '-'", ErrInvalidActionID)
	}
	return nil
}
