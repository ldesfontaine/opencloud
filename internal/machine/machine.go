package machine

import (
	"errors"
	"regexp"
	"time"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/deletion"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// La machine openCloud elle-même : gérée comme les autres, jamais retirée,
// jamais authentifiée par le réseau.
const LocalID = "local"

type Kind string

const (
	KindLocal  Kind = "local"
	KindRemote Kind = "remote"
)

type Machine struct {
	ID           string
	Name         string
	Kind         Kind
	PublicKey    []byte
	Hostname     string
	Address      string
	OS           string
	Arch         string
	AgentVersion string
	EnrolledAt   time.Time
	// Zéro tant que l'agent n'a jamais donné signe de vie.
	LastSeenAt time.Time
	CreatedAt  time.Time
}

func (m Machine) IsLocal() bool {
	return m.Kind == KindLocal
}

// Status est ce que l'opérateur voit : la machine et si elle est en ligne.
type Status struct {
	Machine
	Online bool
}

// Token est un jeton d'enrôlement tel que la base le connaît : sans son clair.
type Token struct {
	ID        string
	Hash      string
	Prefix    string
	Name      string
	MachineID string
	CreatedAt time.Time
	ExpiresAt time.Time
	// Zéro tant que le jeton n'a pas servi.
	ConsumedAt time.Time
	ConsumedBy string
}

// Ce que l'agent envoie pour s'enrôler.
type Enrollment struct {
	MachineID    string
	PublicKey    []byte
	Token        string
	Hostname     string
	Address      string
	OS           string
	Arch         string
	AgentVersion string
	// Empreinte de l'identifiant de machine de l'agent ; vide, pas de
	// contrôle.
	HostFingerprint string
}

var (
	ErrNotFound      = errors.New("machine not found")
	ErrLocalMachine  = errors.New("the local machine cannot be removed or re-enrolled by token")
	ErrNameInvalid   = refusal.Invalid("machine.name_invalid", "machine name must be a lowercase label: letters, digits, dashes")
	ErrNameTaken     = errors.New("machine name already used")
	ErrTokenNotFound = errors.New("enrollment token not found")
	ErrTokenConsumed = errors.New("enrollment token already consumed")
	ErrTokenExpired  = errors.New("enrollment token expired")
	ErrBadKey        = errors.New("public key must be 32 bytes")
	ErrBadID         = errors.New("machine id must be a uuid")
	ErrBadNonce      = errors.New("challenge unknown, expired or already used")
	ErrClockSkew     = errors.New("clock skew exceeds the tolerance")
	ErrBadSignature  = errors.New("invalid signature")
	ErrNotConnected  = errors.New("machine not connected")
	ErrBusy          = errors.New("machine command queue full")
	ErrIsOpenCloud   = errors.New("the agent runs on the " + brand.ProductName + " machine itself")
)

// Ce qui retient le retrait d'une machine : ce qui y est déclaré ou la vise
// serait orphelin, et le supprimer ou le détacher revient à l'opérateur
// (restrict, migration 028_restrict.sql).
var (
	ErrHasDeclaredServices = refusal.Conflict("machine.has_declared_services", "the machine carries declared services")
	ErrHasProbes           = refusal.Conflict("machine.has_probes", "the machine runs probes")
	ErrHasHeartbeats       = refusal.Conflict("machine.has_heartbeats", "heartbeats are attached to the machine")
	ErrShownOnStatus       = refusal.Conflict("machine.shown_on_status", "a status component shows the machine")
)

// RefuseDeletion rend le refus du retrait que le plan retient, nil sinon.
func RefuseDeletion(plan deletion.Plan) error {
	return deletion.Refuse(plan, map[deletion.Kind]error{
		deletion.KindDeclaredServices: ErrHasDeclaredServices,
		deletion.KindProbes:           ErrHasProbes,
		deletion.KindHeartbeats:       ErrHasHeartbeats,
		deletion.KindComponents:       ErrShownOnStatus,
	})
}

// Un nom de machine se lit comme une étiquette DNS : il servira dans des
// noms d'hôte (whoami.vps-paris-1…).
var namePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func ValidateName(name string) error {
	if !namePattern.MatchString(name) {
		return ErrNameInvalid
	}
	return nil
}
