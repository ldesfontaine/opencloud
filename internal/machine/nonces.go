package machine

import (
	"encoding/hex"
	"sync"
	"time"
)

// Un défi vit deux minutes. Une machine n'en garde que quelques-uns en
// attente : un agent n'en demande qu'un par connexion, et qui en réclame
// en boucle pour une machine connue ne fait que remplacer les siens. La
// table reste ainsi bornée par le nombre de machines, et son balayage,
// espacé, ne repasse pas sur toute la table à chaque défi.
const (
	nonceLifetime      = 2 * time.Minute
	noncesPerMachine   = 4
	nonceSweepInterval = 30 * time.Second
)

type issuedNonce struct {
	machineID string
	expiresAt time.Time
}

// nonces garde les défis émis et pas encore relevés ; chacun sert une fois.
type nonces struct {
	mu     sync.Mutex
	issued map[string]issuedNonce
	// Les défis en attente de chaque machine, du plus ancien au plus récent.
	byMachine map[string][]string
	lastSweep time.Time
}

func newNonces() *nonces {
	return &nonces{issued: make(map[string]issuedNonce), byMachine: make(map[string][]string)}
}

func (n *nonces) issue(machineID string, now time.Time) ([]byte, error) {
	nonce, err := NewNonce()
	if err != nil {
		return nil, err
	}
	key := hex.EncodeToString(nonce)
	n.mu.Lock()
	defer n.mu.Unlock()
	if now.Sub(n.lastSweep) >= nonceSweepInterval {
		n.prune(now)
		n.lastSweep = now
	}
	pending := n.byMachine[machineID]
	if len(pending) >= noncesPerMachine {
		delete(n.issued, pending[0])
		pending = pending[1:]
	}
	n.byMachine[machineID] = append(pending, key)
	n.issued[key] = issuedNonce{machineID: machineID, expiresAt: now.Add(nonceLifetime)}
	return nonce, nil
}

// size sert aux tests.
func (n *nonces) size() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.issued)
}

// consume retire le défi et dit s'il avait été émis pour cette machine.
func (n *nonces) consume(nonce []byte, machineID string, now time.Time) bool {
	key := hex.EncodeToString(nonce)
	n.mu.Lock()
	defer n.mu.Unlock()
	issued, ok := n.issued[key]
	if !ok {
		return false
	}
	n.forget(key, issued.machineID)
	return issued.machineID == machineID && now.Before(issued.expiresAt)
}

func (n *nonces) forget(key, machineID string) {
	delete(n.issued, key)
	pending := n.byMachine[machineID]
	for i, candidate := range pending {
		if candidate == key {
			pending = append(pending[:i:i], pending[i+1:]...)
			break
		}
	}
	if len(pending) == 0 {
		delete(n.byMachine, machineID)
		return
	}
	n.byMachine[machineID] = pending
}

func (n *nonces) prune(now time.Time) {
	for key, issued := range n.issued {
		if !now.Before(issued.expiresAt) {
			n.forget(key, issued.machineID)
		}
	}
}
