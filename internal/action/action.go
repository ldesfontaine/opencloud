package action

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

const (
	// OrderLifetime est le temps qu'un ordre a pour arriver et être pris :
	// passé not_after, l'agent le refuse et la boucle de fond le dit expiré.
	OrderLifetime = 2 * time.Minute
	// Retention : un run et sa sortie partent à cet âge.
	Retention = 90 * 24 * time.Hour
	// Une raison tient sur une ligne courte : au-delà, ce n'est pas un agent
	// qui parle.
	maxReasonBytes = 200
	maxRunIDBytes  = 64
)

// State est l'état d'un run. Le serveur pose queued, sent et expired ;
// l'agent rapporte les autres.
type State string

const (
	StateQueued    State = "queued"
	StateSent      State = "sent"
	StateAccepted  State = "accepted"
	StateRefused   State = "refused"
	StateSucceeded State = "succeeded"
	StateFailed    State = "failed"
	StateExpired   State = "expired"
)

// States rend la liste fermée, dans l'ordre de la vie d'un run.
func States() []State {
	return []State{StateQueued, StateSent, StateAccepted, StateRefused, StateSucceeded, StateFailed, StateExpired}
}

// IsFinished : rien ne change plus un run dans cet état.
func (s State) IsFinished() bool {
	switch s {
	case StateRefused, StateSucceeded, StateFailed, StateExpired:
		return true
	}
	return false
}

// ReasonUndelivered : l'ordre écrit n'a pas pu partir (flux tombé, file
// pleine). La seule raison que pose le serveur ; les autres viennent de
// l'agent (agentwire.RefusalReasons).
const ReasonUndelivered = "undelivered"

// Run est un ordre et ce qu'il est devenu. Il survit à sa machine et à son
// agent : MachineID et AgentID se vident, le nom reste.
type Run struct {
	ID          string
	Kind        agentwire.Kind
	Params      json.RawMessage
	MachineID   string
	MachineName string
	AgentID     string
	State       State
	Reason      string
	NotAfter    time.Time
	Seq         int64
	// L'acteur du journal d'audit, recopié : « operator:lucas ».
	RequestedBy string
	RequestedAt time.Time
	// Zéro tant que le run n'est pas fini.
	FinishedAt time.Time
	Output     []LogLine
}

// LogLine est une ligne de la sortie d'un run.
type LogLine struct {
	At     time.Time
	Stream string
	Line   string
}

// Order est ce que la clé du serveur signe pour un run.
type Order struct {
	RunID     string
	MachineID string
	AgentID   string
	Kind      agentwire.Kind
	Params    json.RawMessage
	NotAfter  time.Time
	Seq       int64
}

func (r Run) order() Order {
	return Order{
		RunID: r.ID, MachineID: r.MachineID, AgentID: r.AgentID, Kind: r.Kind,
		Params: r.Params, NotAfter: r.NotAfter, Seq: r.Seq,
	}
}

func (o Order) encode() ([]byte, error) {
	return json.Marshal(agentwire.OrderBody{
		RunID: o.RunID, MachineID: o.MachineID, AgentID: o.AgentID, Kind: o.Kind,
		Params: o.Params, NotAfter: o.NotAfter.Unix(), Seq: o.Seq,
	})
}

// Query choisit les runs d'une liste, du plus récent au plus ancien.
type Query struct {
	MachineID string
	Limit     int
}

var (
	ErrMachineOffline = refusal.Conflict("action.machine_offline", "the machine is offline: an order only leaves through its open stream")
	// ErrReportInvalid : la section actions d'un signal sort de ce qu'un
	// agent peut rapporter.
	ErrReportInvalid = errors.New("action report invalid")
	// ErrNoServerKey : le serveur n'a pas de clé pour signer (la commande
	// mcp, qui n'envoie rien).
	ErrNoServerKey = errors.New("no server key to sign orders")
)
