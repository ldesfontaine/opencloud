package agent

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"github.com/ldesfontaine/opencloud/internal/anchor"
	"github.com/ldesfontaine/opencloud/internal/fsx"
)

// « agent trust » : la seule voie qui change les ancres de confiance locales
// d'un agent enrôlé. Réservée à root, elle écrit le fichier d'identité de
// l'agent avec les mêmes gardes que l'agent applique à son état (CheckStateDir),
// sous le verrou que l'agent prend lui aussi, et le fichier reste à son compte.
// L'agent en marche relit ses ancres avant chaque ordre : pas de redémarrage.

// Les verbes de la commande.
const (
	TrustList   = "list"
	TrustAdd    = "add"
	TrustRemove = "remove"
)

var (
	// ErrTrustNotRoot : la commande change ce que l'agent, root, accepte
	// d'exécuter ; un compte sans privilège ne la lance pas.
	ErrTrustNotRoot = errors.New("agent trust is reserved to root")
	// ErrTrustUnknownKind : le type n'est pas dans le registre.
	ErrTrustUnknownKind = errors.New("unknown anchor kind")
	// ErrTrustUnknownVerb : ni list, ni add, ni remove.
	ErrTrustUnknownVerb = errors.New("unknown trust verb")
)

// TrustOptions est ce que la commande touche : l'état de l'agent, qui la lance,
// et où elle écrit sa trace.
type TrustOptions struct {
	StateDir  *os.Root
	StatePath string
	// EUID est celui du processus ; un test y met le sien.
	EUID int
	// Audit reçoit la phrase du changement pour le journal du système (journald) ;
	// nil : pas de trace. Son échec ne défait pas le changement : il est rendu
	// dans TrustResult.
	Audit func(message string) error
}

// TrustResult est ce que la commande rend : les ancres du fichier après le
// verbe, et l'échec de la trace si elle a échoué.
type TrustResult struct {
	Stored     map[string][]string
	State      anchor.State
	AuditError error
}

// Trust joue un verbe. kindName et value sont ignorés par list.
func Trust(opts TrustOptions, verb, kindName, value string) (TrustResult, error) {
	if opts.EUID != 0 {
		return TrustResult{}, ErrTrustNotRoot
	}
	owner, err := stateOwner(opts.StateDir)
	if err != nil {
		return TrustResult{}, err
	}
	if err := CheckStateDirOwnedBy(opts.StateDir, opts.StatePath, uint32(owner.UID)); err != nil { // #nosec G115 -- un uid de stat.
		return TrustResult{}, err
	}
	if verb == TrustList {
		identity, err := LoadIdentity(opts.StateDir)
		if err != nil {
			return TrustResult{}, err
		}
		return TrustResult{Stored: identity.Anchors, State: identity.AnchorState()}, nil
	}
	if verb != TrustAdd && verb != TrustRemove {
		return TrustResult{}, ErrTrustUnknownVerb
	}
	kind, known := anchor.Lookup(kindName)
	if !known {
		return TrustResult{}, ErrTrustUnknownKind
	}
	identity, err := UpdateIdentity(opts.StateDir, &owner, func(identity *Identity) error {
		changed, err := changeAnchors(identity.Anchors, verb, kind, value)
		identity.Anchors = changed
		return err
	})
	if err != nil {
		return TrustResult{}, err
	}
	result := TrustResult{Stored: identity.Anchors, State: identity.AnchorState()}
	if opts.Audit != nil {
		result.AuditError = opts.Audit(fmt.Sprintf("trust %s %s %s (uid %d)", verb, kind.Name, value, opts.EUID))
	}
	return result, nil
}

func changeAnchors(stored map[string][]string, verb string, kind anchor.Kind, value string) (map[string][]string, error) {
	if verb == TrustAdd {
		return anchor.Add(stored, kind, value)
	}
	return anchor.Remove(stored, kind, value)
}

// stateOwner rend le compte à qui appartient l'état : c'est à lui que le
// fichier réécrit par root reste donné.
func stateOwner(stateDir *os.Root) (fsx.Owner, error) {
	info, err := stateDir.Stat(".")
	if err != nil {
		return fsx.Owner{}, fmt.Errorf("stat state dir: %w", err)
	}
	raw, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fsx.Owner{}, errors.New("no owner reported for the state dir")
	}
	return fsx.Owner{UID: int(raw.Uid), GID: int(raw.Gid)}, nil
}
