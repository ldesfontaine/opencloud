package agent

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/ldesfontaine/opencloud/internal/fsx"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

const (
	identityFile = "identity.json"
	identityMode = 0o600
)

// Identity est ce que l'agent garde entre deux démarrages : sa clé, l'id que
// le serveur lui a reconnu, et où le joindre.
type Identity struct {
	MachineID  string    `json:"machine_id"`
	PublicKey  []byte    `json:"public_key"`
	PrivateKey []byte    `json:"private_key"`
	Server     string    `json:"server"`
	Pin        string    `json:"pin,omitempty"`
	EnrolledAt time.Time `json:"enrolled_at"`
}

var ErrNotEnrolled = errors.New("agent not enrolled: run with -server and -token")

// NewIdentity tire une clé et un id ; rien n'est écrit avant l'enrôlement.
func NewIdentity(server, pin string) (Identity, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Identity{}, fmt.Errorf("generate key: %w", err)
	}
	id, err := machine.NewID()
	if err != nil {
		return Identity{}, fmt.Errorf("generate id: %w", err)
	}
	return Identity{MachineID: id, PublicKey: publicKey, PrivateKey: privateKey, Server: server, Pin: pin}, nil
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
