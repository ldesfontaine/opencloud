package machine

import (
	"encoding/hex"
	"sync"
	"time"
)

const nonceLifetime = 2 * time.Minute

type issuedNonce struct {
	machineID string
	expiresAt time.Time
}

// nonces garde les défis émis et pas encore relevés ; chacun sert une fois.
type nonces struct {
	mu     sync.Mutex
	issued map[string]issuedNonce
}

func newNonces() *nonces {
	return &nonces{issued: make(map[string]issuedNonce)}
}

func (n *nonces) issue(machineID string, now time.Time) ([]byte, error) {
	nonce, err := NewNonce()
	if err != nil {
		return nil, err
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.prune(now)
	n.issued[hex.EncodeToString(nonce)] = issuedNonce{machineID: machineID, expiresAt: now.Add(nonceLifetime)}
	return nonce, nil
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
	delete(n.issued, key)
	return issued.machineID == machineID && now.Before(issued.expiresAt)
}

func (n *nonces) prune(now time.Time) {
	for key, issued := range n.issued {
		if !now.Before(issued.expiresAt) {
			delete(n.issued, key)
		}
	}
}
