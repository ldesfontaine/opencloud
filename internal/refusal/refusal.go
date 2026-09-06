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
