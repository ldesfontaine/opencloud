package verbrequest

import (
	"errors"
	"time"

	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// State est où en est une demande. Seule « awaiting_confirmation » attend
// quelqu'un ; les trois autres sont des fins.
type State string

const (
	StateAwaiting  State = "awaiting_confirmation"
	StateConfirmed State = "confirmed"
	StateRejected  State = "rejected"
	StateExpired   State = "expired"
)

// States rend la liste fermée des états, dans l'ordre de la vie d'une demande.
func States() []State {
	return []State{StateAwaiting, StateConfirmed, StateRejected, StateExpired}
}

const (
	// TTL est le temps laissé à l'opérateur : passé ce délai, la demande est
	// échue et ne se confirme plus.
	TTL = 15 * time.Minute
	// Retention est le temps que la ligne d'une demande terminée reste lisible ;
	// le journal d'audit garde la trace bien plus longtemps.
	Retention = 30 * 24 * time.Hour
	// MaxPendingPerToken borne les demandes en attente d'un même jeton : un
	// agent qui boucle ne noie pas l'écran de l'opérateur.
	MaxPendingPerToken = 10
	// SweepEvery est la cadence du passage qui échoit et purge.
	SweepEvery = time.Minute
)

// Request est une demande de verbe. Params porte les segments {…} du chemin
// de la route du verbe, Body le corps JSON tel que le jeton l'a envoyé (vide
// sans corps) : confirmer rejoue la route avec exactement eux.
type Request struct {
	ID         string
	Verb       string
	ObjectName string
	Params     map[string]string
	Body       string
	// Le jeton qui a demandé, son nom d'alors, et pour qui il dit appeler.
	TokenID    string
	TokenName  string
	OnBehalfOf string
	State      State
	CreatedAt  time.Time
	ExpiresAt  time.Time
}

// ObjectID est l'objet que le verbe vise : le segment {id} de son chemin.
func (r Request) ObjectID() string {
	return r.Params["id"]
}

var (
	ErrNotFound = errors.New("verb request not found")

	ErrTooMany = refusal.Conflict("verb_request.too_many", "too many requests awaiting a confirmation for this token")
	ErrExpired = refusal.Conflict("verb_request.expired", "the request expired before it was confirmed")
	ErrDecided = refusal.Conflict("verb_request.already_decided", "the request was already confirmed or rejected")
)
