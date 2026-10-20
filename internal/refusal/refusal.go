package refusal

import "errors"

// Error est un refus. Il se déclare en sentinelle, une fois par cause :
// errors.Is le reconnaît par son adresse, comme une erreur ordinaire.
type Error struct {
	key        string
	message    string
	isConflict bool
}

// Invalid déclare un refus de la demande elle-même : un champ faux, une
// limite dépassée.
func Invalid(key, message string) *Error {
	return &Error{key: key, message: message}
}

// Conflict déclare un refus dû à l'état présent : la même demande passerait
// ailleurs ou plus tard.
func Conflict(key, message string) *Error {
	return &Error{key: key, message: message, isConflict: true}
}

// Error rend le texte du journal ; l'opérateur, lui, lit la clé.
func (e *Error) Error() string {
	return e.message
}

// KeyOf rend la clé du premier refus de la chaîne. ok est faux pour une
// erreur qui n'est pas un refus : elle n'a pas de clé, et son texte ne doit
// pas remonter à l'opérateur.
func KeyOf(err error) (key string, ok bool) {
	var refused *Error
	if !errors.As(err, &refused) {
		return "", false
	}
	return refused.key, true
}

// IsConflict dit si le premier refus de la chaîne vient de l'état présent.
func IsConflict(err error) bool {
	var refused *Error
	return errors.As(err, &refused) && refused.isConflict
}
