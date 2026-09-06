// Package refusal porte le type Refusal des conventions : un refus n'est pas
// une erreur. Il dit la cause et le geste qui le lève, en français, tels que
// l'opérateur les lit ; le binaire sort avec le code 2.
package refusal

import "fmt"

// Refusal : ce qu'on n'a pas voulu faire, et pourquoi. Remedy dit quoi faire.
type Refusal struct {
	Cause  string
	Remedy string
}

func (r Refusal) Error() string {
	return fmt.Sprintf("refus : %s\n→ %s", r.Cause, r.Remedy)
}
