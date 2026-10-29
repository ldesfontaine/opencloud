package rollback

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/fsx"
	"github.com/ldesfontaine/opencloud/internal/release"
)

const (
	// GracePeriod est le temps que la nouvelle version a pour rejoindre le
	// serveur et être acquittée. Trois minutes : un redémarrage de l'agent
	// (dix secondes de RestartSec), une reconnexion avec son délai croissant et
	// une machine un peu lente tiennent largement dedans, et une machine sans
	// agent ne reste pas plus de trois minutes sans que le retour s'engage.
	// Le serveur ne le règle pas : il n'est ni dans l'ordre ni dans le
	// marqueur qu'un autre que l'agent écrit.
	GracePeriod = 3 * time.Minute
	// SettleAfter est le temps que la version d'avant doit tenir, une fois
	// revenue, pour que sa remise en état compte : plus tôt, elle est cassée
	// elle aussi.
	SettleAfter = time.Minute

	maxMarkerBytes = 4 << 10
	markerMode     = 0o600
)

// State dit où en est la mise à jour que le marqueur suit.
type State string

const (
	// La bascule est faite, la nouvelle version n'a pas encore confirmé.
	StatePending State = "pending"
	// Un retour arrière manuel que la version d'avant a confirmé : il reste à
	// le dire au serveur.
	StateConfirmed State = "confirmed"
	// L'échéance est passée : le lanceur a rebasculé sur la version d'avant,
	// qui doit encore le dire au serveur.
	StateRolledBack State = "rolled_back"
	// La remise en état n'a pas pu se faire, ou la version d'avant a planté à
	// son tour : on s'arrête là, jamais de boucle.
	StateFailed State = "failed"
)

// Kind dit ce qui a basculé le lien : une mise à jour (qui se rattrape seule)
// ou un retour arrière demandé (qui ne se rattrape pas).
type Kind string

const (
	KindUpdate   Kind = "update"
	KindRollback Kind = "rollback"
)

// Cause dit pourquoi le marqueur n'est plus « pending ».
type Cause string

const (
	// Aucune confirmation avant l'échéance, sans que le binaire ait planté.
	CauseTimeout Cause = "timeout"
	// Le binaire a planté, ou ne s'est pas lancé, avant l'échéance.
	CauseCrash Cause = "crash"
	// La version d'avant n'est plus là : rien à rebasculer.
	CausePreviousMissing Cause = "previous_missing"
	// La version d'avant a planté à son tour juste après la remise en état.
	CausePreviousCrash Cause = "previous_crash"
	// Un retour arrière manuel qu'aucune version n'a confirmé.
	CauseUnconfirmed Cause = "unconfirmed"
)

// Marker est écrit dans l'état de l'agent AVANT la bascule du lien.
type Marker struct {
	State State `json:"state"`
	Kind  Kind  `json:"kind"`
	// L'ordre qui a lancé la bascule : c'est à lui que l'agent répond.
	RunID string `json:"run_id"`
	// La version qui doit tourner après la bascule, et celle d'avant, vers
	// laquelle on revient (vX.Y.Z).
	Target    string    `json:"target"`
	Previous  string    `json:"previous"`
	StartedAt time.Time `json:"started_at"`
	Deadline  time.Time `json:"deadline"`
	// Les arrêts anormaux comptés par le lanceur avant l'échéance.
	Crashes    int       `json:"crashes"`
	Cause      Cause     `json:"cause,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitzero"`
}

var (
	// ErrNoMarker : aucune mise à jour n'attend.
	ErrNoMarker = errors.New("no update marker")
	// ErrCorruptMarker : le fichier existe et ne se lit pas ; il n'est cru de
	// personne.
	ErrCorruptMarker = errors.New("update marker unreadable")
)

// Validate refuse un marqueur qui ne dit pas une mise à jour possible : un
// état ou un genre inconnu, une version qui n'est pas vX.Y.Z (elle entre dans
// un nom de fichier), un run absent.
func (m Marker) Validate() error {
	switch m.State {
	case StatePending, StateConfirmed, StateRolledBack, StateFailed:
	default:
		return fmt.Errorf("state %q", m.State)
	}
	if m.Kind != KindUpdate && m.Kind != KindRollback {
		return fmt.Errorf("kind %q", m.Kind)
	}
	if m.RunID == "" || len(m.RunID) > 64 {
		return errors.New("run id")
	}
	for _, version := range []string{m.Target, m.Previous} {
		if _, err := release.ParseVersion(version); err != nil {
			return err
		}
	}
	if m.Target == m.Previous {
		return errors.New("target equals previous")
	}
	return nil
}

// IsDue : la confirmation n'est pas venue à l'échéance.
func (m Marker) IsDue(now time.Time) bool {
	return m.State == StatePending && !now.Before(m.Deadline)
}

// Load lit le marqueur : ErrNoMarker s'il n'y en a pas, ErrCorruptMarker
// (enveloppé) s'il existe et ne se lit pas.
func Load(state *os.Root) (Marker, error) {
	content, err := fsx.ReadPrivate(state, brand.AgentUpdateMarker, maxMarkerBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return Marker{}, ErrNoMarker
	}
	if err != nil {
		return Marker{}, fmt.Errorf("%w: %w", ErrCorruptMarker, err)
	}
	if len(content) > maxMarkerBytes {
		return Marker{}, fmt.Errorf("%w: more than %d bytes", ErrCorruptMarker, maxMarkerBytes)
	}
	var marker Marker
	if err := json.Unmarshal(content, &marker); err != nil {
		return Marker{}, fmt.Errorf("%w: %w", ErrCorruptMarker, err)
	}
	if err := marker.Validate(); err != nil {
		return Marker{}, fmt.Errorf("%w: %w", ErrCorruptMarker, err)
	}
	return marker, nil
}

// Save écrit le marqueur d'un coup (fichier temporaire, fsync, rename) : une
// coupure laisse l'ancien ou le nouveau, jamais la moitié de l'un.
func Save(state *os.Root, marker Marker) error {
	if err := marker.Validate(); err != nil {
		return fmt.Errorf("save update marker: %w", err)
	}
	content, err := json.Marshal(marker)
	if err != nil {
		return fmt.Errorf("encode update marker: %w", err)
	}
	if err := fsx.WriteAtomic(state, brand.AgentUpdateMarker, content, markerMode); err != nil {
		return fmt.Errorf("write update marker: %w", err)
	}
	return nil
}

// Remove retire le marqueur ; retirer ce qui n'existe pas n'est pas une erreur.
func Remove(state *os.Root) error {
	if err := state.Remove(brand.AgentUpdateMarker); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove update marker: %w", err)
	}
	if err := fsx.SyncDir(state, "."); err != nil {
		return fmt.Errorf("sync state dir: %w", err)
	}
	return nil
}
