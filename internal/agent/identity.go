package agent

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/anchor"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/fsx"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

const (
	identityFile = brand.AgentIdentityFile
	identityMode = 0o600
	// Le verrou des écritures de l'identité (UpdateIdentity).
	identityLockFile = identityFile + ".lock"
)

// Identity est ce que l'agent garde entre deux démarrages : sa clé, l'id que
// le serveur lui a reconnu, et où le joindre.
type Identity struct {
	MachineID  string `json:"machine_id"`
	PublicKey  []byte `json:"public_key"`
	PrivateKey []byte `json:"private_key"`
	Server     string `json:"server"`
	Pin        string `json:"pin,omitempty"`
	// Langue de l'opérateur, choisie sur openCloud au moment de l'installation.
	Language   lang.Code `json:"language,omitempty"`
	EnrolledAt time.Time `json:"enrolled_at"`
	// Les droits cochés par l'opérateur, écrits par la ligne d'installation
	// (-rights), un par entrée (« observe », « files.write=/srv/a:/srv/b »).
	// Absents chez un agent d'avant les droits : observe seul. Rapportés au
	// serveur à chaque ouverture du flux, et appliqués : un ordre dont le
	// droit n'est pas ici est refusé (orders.go). Les changer, c'est
	// ré-enrôler avec d'autres droits.
	Rights []string `json:"rights,omitempty"`
	// L'agent que le serveur a inscrit à l'enrôlement : un ordre pour un
	// autre agent de la même machine (avant un ré-enrôlement) est refusé.
	AgentID string `json:"agent_id,omitempty"`
	// La clé publique du serveur, épinglée à l'enrôlement : seul ce qu'elle
	// signe est exécuté. Vide chez un agent qui ne l'a jamais reçue : il
	// refuse tout ordre, et le ré-enrôlement la pose.
	ServerKey []byte `json:"server_key,omitempty"`
	// Le rang du dernier ordre exécuté : un rang qui ne le dépasse pas est
	// un rejeu. Écrit avant d'exécuter, pour qu'un redémarrage ne rouvre
	// pas la porte.
	LastOrderSeq int64 `json:"last_order_seq,omitempty"`
	// La racine des services déployés, choisie au premier lancement
	// (-root-dir). Vide chez un agent d'avant le déploiement : la racine par
	// défaut. Redite au serveur à chaque ouverture du flux.
	RootDir string `json:"root_dir,omitempty"`
	// Les ancres de confiance locales (internal/anchor) : par type, les valeurs
	// que l'administrateur de la machine a posées au premier lancement ou par
	// « agent trust ». Aucun ordre ne les écrit. Rapportées au serveur à chaque
	// ouverture du flux, relues du disque avant chaque ordre.
	Anchors map[string][]string `json:"anchors,omitempty"`
	// L'état est mal gardé (CheckStateDir) : seuls les droits d'observer
	// s'appliquent, ceux du fichier restent sur le disque. Non exporté, donc
	// jamais écrit : l'état corrigé, un redémarrage les rend.
	narrowed bool
}

// Root rend la racine des services que cet agent écrit.
func (id Identity) Root() string {
	if id.RootDir == "" {
		return brand.WorkspaceRoot
	}
	return id.RootDir
}

// ServerKeyFingerprint rend l'empreinte de la clé épinglée, vide sans clé :
// c'est ce que l'agent redit au serveur à chaque ouverture du flux.
func (id Identity) ServerKeyFingerprint() string {
	if len(id.ServerKey) != ed25519.PublicKeySize {
		return ""
	}
	return agentwire.KeyFingerprint(id.ServerKey)
}

// Capabilities rend les droits du fichier ; une liste illisible (fichier
// modifié à la main) vaut observe seul, le plus étroit, comme un état mal
// gardé.
func (id Identity) Capabilities() []machine.Capability {
	if id.narrowed {
		return []machine.Capability{{Name: machine.CapabilityObserve}}
	}
	capabilities, err := machine.ParseCapabilities(strings.Join(id.Rights, ","))
	if err != nil {
		return []machine.Capability{{Name: machine.CapabilityObserve}}
	}
	return capabilities
}

// AnchorState rend les ancres du fichier telles que l'agent les applique : un
// type dont la liste ne se lit pas est illisible et ne sert à rien.
func (id Identity) AnchorState() anchor.State {
	return anchor.FromFile(id.Anchors)
}

// rightsOf écrit des droits sous la forme du fichier : une entrée par droit.
func rightsOf(capabilities []machine.Capability) []string {
	rights := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		rights = append(rights, machine.FormatCapabilities([]machine.Capability{capability}))
	}
	return rights
}

var ErrNotEnrolled = errors.New("agent not enrolled: run with -server and send the token on stdin")

// ErrTokenMissing : l'agent doit s'enrôler et l'entrée standard n'a donné
// aucun jeton.
var ErrTokenMissing = errors.New("no enrollment token on stdin")

// NewIdentity tire une clé et un id ; rien n'est écrit avant l'enrôlement.
func NewIdentity(server, pin string, language lang.Code, capabilities []machine.Capability) (Identity, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Identity{}, fmt.Errorf("generate key: %w", err)
	}
	id, err := machine.NewID()
	if err != nil {
		return Identity{}, fmt.Errorf("generate id: %w", err)
	}
	return Identity{
		MachineID: id, PublicKey: publicKey, PrivateKey: privateKey, Server: server, Pin: pin, Language: language,
		Rights: rightsOf(capabilities),
	}, nil
}

func LoadIdentity(stateDir *os.Root) (Identity, error) {
	content, err := stateDir.ReadFile(identityFile)
	if errors.Is(err, fs.ErrNotExist) {
		return Identity{}, ErrNotEnrolled
	}
	if err != nil {
		return Identity{}, fmt.Errorf("read identity: %w", err)
	}
	var identity Identity
	if err := json.Unmarshal(content, &identity); err != nil {
		return Identity{}, fmt.Errorf("parse identity: %w", err)
	}
	return identity, nil
}

func SaveIdentity(stateDir *os.Root, identity Identity) error {
	content, err := json.Marshal(identity) // #nosec G117 -- l'identité porte sa propre clé privée, en 0600, c'est son rôle.
	if err != nil {
		return fmt.Errorf("encode identity: %w", err)
	}
	if err := fsx.WriteAtomic(stateDir, identityFile, content, identityMode); err != nil {
		return fmt.Errorf("write identity: %w", err)
	}
	return nil
}

// saveKeepingAnchors écrit l'identité que l'agent porte en mémoire, sous le
// verrou, sans effacer les ancres que « agent trust » a posées depuis : les
// ancres du disque l'emportent, et l'identité écrite les porte. Un fichier
// absent (un test) s'écrit tel quel.
func saveKeepingAnchors(stateDir *os.Root, identity Identity) (Identity, error) {
	unlock, err := lockIdentity(stateDir, nil)
	if err != nil {
		return Identity{}, err
	}
	defer unlock()
	if stored, err := LoadIdentity(stateDir); err == nil {
		identity.Anchors = stored.Anchors
	}
	return identity, SaveIdentity(stateDir, identity)
}

// UpdateIdentity relit l'identité du disque, y applique change et la réécrit,
// sous le verrou du fichier : l'agent (le rang d'un ordre, la clé du serveur)
// et « agent trust » (les ancres) ne s'écrasent jamais l'un l'autre. Rend
// l'identité écrite. Quand owner n'est pas nil, le fichier lui est donné.
func UpdateIdentity(stateDir *os.Root, owner *fsx.Owner, change func(*Identity) error) (Identity, error) {
	unlock, err := lockIdentity(stateDir, owner)
	if err != nil {
		return Identity{}, err
	}
	defer unlock()
	identity, err := LoadIdentity(stateDir)
	if err != nil {
		return Identity{}, err
	}
	if err := change(&identity); err != nil {
		return Identity{}, err
	}
	if owner == nil {
		return identity, SaveIdentity(stateDir, identity)
	}
	content, err := json.Marshal(identity) // #nosec G117 -- l'identité porte sa propre clé privée, en 0600, c'est son rôle.
	if err != nil {
		return Identity{}, fmt.Errorf("encode identity: %w", err)
	}
	if err := fsx.WriteAtomicOwned(stateDir, identityFile, content, identityMode, *owner); err != nil {
		return Identity{}, fmt.Errorf("write identity: %w", err)
	}
	return identity, nil
}

// lockIdentity prend le verrou exclusif du fichier d'identité (flock sur un
// fichier à côté) ; unlock le rend. Le verrou tombe avec le processus.
func lockIdentity(stateDir *os.Root, owner *fsx.Owner) (unlock func(), err error) {
	lock, err := stateDir.OpenFile(identityLockFile, os.O_RDWR|os.O_CREATE, identityMode)
	if err != nil {
		return nil, fmt.Errorf("open identity lock: %w", err)
	}
	// Créé par root, le verrou appartient pourtant au compte de l'agent : sans
	// quoi l'agent ne pourrait plus l'ouvrir.
	if owner != nil {
		if err := lock.Chown(owner.UID, owner.GID); err != nil {
			_ = lock.Close()
			return nil, fmt.Errorf("chown identity lock: %w", err)
		}
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil { // #nosec G115 -- un descripteur tient dans un int.
		_ = lock.Close()
		return nil, fmt.Errorf("lock identity: %w", err)
	}
	return func() { _ = lock.Close() }, nil
}

func (id Identity) sign(payload []byte) []byte {
	return machine.Sign(ed25519.PrivateKey(id.PrivateKey), payload)
}
