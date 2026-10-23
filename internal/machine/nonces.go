package machine

import (
	"sync"
	"time"
)

// Protocole 2 : l'agent tire son aléa et signe (aléa, machine, horodatage) ;
// le serveur n'émet plus de défi. L'horodatage borne le rejeu à
// maxClockSkew de part et d'autre ; dans cette fenêtre, le serveur retient
// chaque couple (machine, aléa) déjà servi, le temps que l'horodatage
// signé avec lui sorte de la tolérance (nonceMemory). Un couple n'y entre
// qu'après une signature vérifiée : l'ensemble ne grossit que par les
// connexions légitimes, un anonyme n'y met rien.
//
// L'ensemble vit en mémoire : un redémarrage du serveur l'oublie, et une
// preuve vue juste avant reste rejouable jusqu'à la fin de sa tolérance,
// par qui a pu la lire en clair (le mandataire TLS). Elle n'ouvre qu'un
// flux : les ordres restent signés par le serveur, et rien ne se signe au
// nom de l'agent.
const (
	nonceMemory        = 2 * maxClockSkew
	nonceSweepInterval = 30 * time.Second
)

type nonceKey struct {
	machineID string
	nonce     [NonceSize]byte
}

type nonces struct {
	mu        sync.Mutex
	seen      map[nonceKey]time.Time
	lastSweep time.Time
}

func newNonces() *nonces {
	return &nonces{seen: make(map[nonceKey]time.Time)}
}

// remember inscrit le couple et dit si c'est la première fois. Le contrôle
// et l'inscription se font sous le même verrou : deux preuves identiques
// arrivées ensemble, une seule passe. À n'appeler qu'une fois la signature
// vérifiée.
func (n *nonces) remember(machineID string, nonce []byte, now time.Time) bool {
	key := nonceKey{machineID: machineID}
	copy(key.nonce[:], nonce)
	n.mu.Lock()
	defer n.mu.Unlock()
	if now.Sub(n.lastSweep) >= nonceSweepInterval {
		n.sweep(now)
		n.lastSweep = now
	}
	if _, isSeen := n.seen[key]; isSeen {
		return false
	}
	n.seen[key] = now.Add(nonceMemory)
	return true
}

// sweep oublie les couples dont l'horodatage est sorti de la tolérance :
// rejoués, ils seraient refusés pour l'heure.
func (n *nonces) sweep(now time.Time) {
	for key, forgetAt := range n.seen {
		if !now.Before(forgetAt) {
			delete(n.seen, key)
		}
	}
}
