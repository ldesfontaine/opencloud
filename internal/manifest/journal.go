package manifest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"
)

// ApplyState est où en est un passage d'apply. « planned » (un plan mis de
// côté) viendra : le code valide la liste, la base ne la contraint pas.
type ApplyState string

const (
	// ApplyConfirmed : l'opérateur a confirmé, l'écriture a commencé.
	ApplyConfirmed ApplyState = "confirmed"
	ApplyApplied   ApplyState = "applied"
	ApplyFailed    ApplyState = "failed"
)

func ApplyStates() []ApplyState {
	return []ApplyState{ApplyConfirmed, ApplyApplied, ApplyFailed}
}

// ActorKind dit qui a lancé l'apply : la commande locale par la socket, ou
// l'opérateur par l'interface. Des agents et des règles viendront.
type ActorKind string

const (
	ActorCLI      ActorKind = "cli"
	ActorOperator ActorKind = "operator"
)

func ActorKinds() []ActorKind {
	return []ActorKind{ActorCLI, ActorOperator}
}

// Actor est qui lance l'apply. Le nom est indicatif : le compte système que
// la commande déclare, l'identité transmise par l'authentification. Il n'est
// pas une preuve.
type Actor struct {
	Kind ActorKind
	Name string
}

// ApplyPrefix commence l'identifiant d'un passage.
const ApplyPrefix = "apl_"

// Apply est une ligne du journal : un passage confirmé, ce qu'il devait
// changer, et comment il s'est fini. Ni le fichier ni aucun secret.
type Apply struct {
	ID         string
	At         time.Time
	FinishedAt time.Time
	Actor      Actor
	FileHash   string
	FileName   string
	Prune      bool
	PlanHash   string
	State      ApplyState
	Created    int
	Updated    int
	Deleted    int
	Unchanged  int
	// Done compte les changements écrits, mis à jour après chacun.
	Done int
	// Error est la clé du catalogue de l'arrêt ; ErrorObject, le changement
	// qui l'a subi, « genre/nom ».
	Error       string
	ErrorObject string
	// Changes est le plan confirmé, dans l'ordre d'écriture. Les listes du
	// journal ne le portent pas.
	Changes []Change
}

// ApplyEnd est ce qui ferme une ligne.
type ApplyEnd struct {
	State       ApplyState
	FinishedAt  time.Time
	Done        int
	Error       string
	ErrorObject string
}

var ErrApplyNotFound = errors.New("apply not found")

// Journal est ce que le fichier attend de la base : les passages d'apply.
type Journal interface {
	InsertApply(ctx context.Context, apply Apply) error
	// SetApplyProgress dit combien de changements sont écrits.
	SetApplyProgress(ctx context.Context, id string, done int) error
	FinishApply(ctx context.Context, id string, end ApplyEnd) error
	// ListApplies rend les passages du plus récent au plus ancien, sans leur
	// plan.
	ListApplies(ctx context.Context, limit int) ([]Apply, error)
	// GetApply rend un passage avec son plan ; ErrApplyNotFound sinon.
	GetApply(ctx context.Context, id string) (Apply, error)
	// FailInterruptedApplies passe en « failed » les lignes restées
	// « confirmed » : un arrêt brutal les a laissées. Rend leur nombre.
	FailInterruptedApplies(ctx context.Context, finishedAt time.Time, key string) (int, error)
}

func newApplyID() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return ApplyPrefix + hex.EncodeToString(raw), nil
}
