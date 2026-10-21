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
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/fsx"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

const (
	identityFile = "identity.json"
	identityMode = 0o600
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
// modifié à la main) vaut observe seul, le plus étroit.
func (id Identity) Capabilities() []machine.Capability {
	capabilities, err := machine.ParseCapabilities(strings.Join(id.Rights, ","))
	if err != nil {
		return []machine.Capability{{Name: machine.CapabilityObserve}}
	}
	return capabilities
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

func (id Identity) sign(payload []byte) []byte {
	return machine.Sign(ed25519.PrivateKey(id.PrivateKey), payload)
}
