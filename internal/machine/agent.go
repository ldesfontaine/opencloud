package machine

import (
	"time"

	"github.com/ldesfontaine/opencloud/internal/ident"
)

// Agent est le logiciel installé sur une machine : sa clé, sa version, son
// dernier signe de vie, ses droits. Ré-enrôler en installe un nouveau et
// révoque l'ancien ; la machine, elle, reste.
type Agent struct {
	ID         string
	MachineID  string
	PublicKey  []byte
	Version    string
	RootDir    string
	EnrolledAt time.Time
	// Zéro tant qu'il n'a donné aucun signe de vie.
	LastSeenAt time.Time
	// Zéro tant qu'il n'est pas révoqué.
	RevokedAt time.Time
	// Ce que l'agent rapporte de son fichier à l'ouverture de son flux ;
	// vide tant qu'il n'a rien rapporté (un agent d'avant les droits).
	Capabilities []Capability
	ReportedAt   time.Time
	// Ce que le jeton qui l'a enrôlé demandait ; vide pour un agent d'avant
	// les droits.
	Requested []Capability
}

func (a Agent) Exists() bool {
	return a.ID != ""
}

func (a Agent) IsRevoked() bool {
	return !a.RevokedAt.IsZero()
}

// IsActive : un agent installé et pas révoqué, le seul qui peut se
// connecter.
func (a Agent) IsActive() bool {
	return a.Exists() && !a.IsRevoked()
}

// CapabilitiesDiffer dit si le fichier de l'agent ne dit pas ce que le jeton
// demandait. Rien à comparer tant que l'un des deux manque.
func (a Agent) CapabilitiesDiffer() bool {
	if len(a.Requested) == 0 || a.ReportedAt.IsZero() {
		return false
	}
	return !SameCapabilities(a.Requested, a.Capabilities)
}

// Le préfixe rend un identifiant d'agent lisible dans un journal, jamais
// confondu avec celui d'une machine.
const agentIDPrefix = "agt_"

func NewAgentID() (string, error) {
	id, err := ident.New()
	if err != nil {
		return "", err
	}
	return agentIDPrefix + id, nil
}
