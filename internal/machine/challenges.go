package machine

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"sync"
	"time"
)

// Le défi que l'agent signe est sans état : le serveur le signe lui-même
// (HMAC) et ne garde rien de ce qu'il émet. /agent/challenge est ouvert à
// tous ; une table de défis en attente, même plafonnée par machine, laisse
// un anonyme qui connaît l'identifiant d'une machine évincer le défi de son
// agent. Sans table, il n'y a rien à évincer : demander des défis en boucle
// ne change rien à ceux des autres.
//
// Un défi tient dans NonceSize octets, la seule taille que les agents
// installés acceptent de signer (SignedPayload). Il se compose de
// trois morceaux, dans cet ordre : un aléa de 8 octets, l'expiration sur 8
// octets, le MAC sur les 16 derniers.
//
// L'aléa distingue deux défis émis pour la même machine à la même seconde.
// L'expiration est un horodatage Unix en secondes, big-endian, couvert par le
// MAC avec le reste. Le MAC est HMAC-SHA256 de l'étiquette, de la machine,
// de l'aléa et de l'expiration, tronqué à 128 bits : on ne le devine pas dans
// les deux minutes que vit un défi, et la troncature laisse la place à l'aléa.
//
// La clé est tirée au démarrage et ne quitte pas la mémoire : un redémarrage
// invalide les défis en cours, et l'agent, qui reçoit alors
// challenge_expired, en redemande un.
//
// Contre le rejeu, le serveur retient les défis déjà utilisés jusqu'à leur
// expiration. Un défi n'y entre qu'après une signature vérifiée : l'ensemble
// ne grossit que par les connexions légitimes, un anonyme n'y met rien.
const (
	challengeLifetime      = 2 * time.Minute
	challengeSweepInterval = 30 * time.Second

	challengeRandomSize = 8
	challengeExpirySize = 8
	challengeMACSize    = NonceSize - challengeRandomSize - challengeExpirySize

	challengeKeySize = 32
	// Sépare ce MAC de tout autre usage de la même clé.
	challengeLabel = "agent-challenge/v1"
)

// usedChallenge est un défi sous forme de tableau, pour servir de clé à
// l'ensemble des défis utilisés.
type usedChallenge [NonceSize]byte

// challenges émet et vérifie les défis, et retient ceux qui ont servi.
type challenges struct {
	key []byte

	mu        sync.Mutex
	used      map[usedChallenge]time.Time
	lastSweep time.Time
}

func newChallenges() *challenges {
	key := make([]byte, challengeKeySize)
	// Depuis Go 1.24, rand.Read ne rend jamais d'erreur : il arrête le
	// programme plutôt que de rendre un tirage qui ne serait pas aléatoire.
	_, _ = rand.Read(key)
	return &challenges{key: key, used: make(map[usedChallenge]time.Time)}
}

// issue signe un défi pour la machine ; rien n'est gardé.
func (c *challenges) issue(machineID string, now time.Time) []byte {
	challenge := make([]byte, NonceSize)
	_, _ = rand.Read(challenge[:challengeRandomSize])
	expiresAt := now.Add(challengeLifetime).Unix()
	binary.BigEndian.PutUint64(challenge[challengeRandomSize:], uint64(expiresAt)) // #nosec G115 -- un horodatage Unix actuel est positif.
	copy(challenge[challengeRandomSize+challengeExpirySize:], c.mac(machineID, challenge[:challengeRandomSize+challengeExpirySize]))
	return challenge
}

// valid dit si le défi a été signé par ce serveur pour cette machine et
// n'a pas expiré. Il ne dit rien du rejeu : voir markUsed.
func (c *challenges) valid(challenge []byte, machineID string, now time.Time) bool {
	if len(challenge) != NonceSize {
		return false
	}
	signed := challenge[:challengeRandomSize+challengeExpirySize]
	if !hmac.Equal(challenge[len(signed):], c.mac(machineID, signed)) {
		return false
	}
	return now.Before(expiryOf(challenge))
}

// markUsed inscrit le défi et dit si c'est la première fois. La vérification
// et l'inscription se font sous le même verrou : deux preuves identiques
// arrivées ensemble, une seule passe. À n'appeler qu'une fois la signature
// vérifiée, et sur un défi que valid a accepté.
func (c *challenges) markUsed(challenge []byte, now time.Time) bool {
	var key usedChallenge
	copy(key[:], challenge)
	c.mu.Lock()
	defer c.mu.Unlock()
	if now.Sub(c.lastSweep) >= challengeSweepInterval {
		c.sweep(now)
		c.lastSweep = now
	}
	if _, seen := c.used[key]; seen {
		return false
	}
	c.used[key] = expiryOf(challenge)
	return true
}

// size sert aux tests.
func (c *challenges) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.used)
}

// sweep oublie les défis utilisés qui ont expiré : valid les refuse de toute
// façon, il n'y a plus de rejeu à empêcher.
func (c *challenges) sweep(now time.Time) {
	for key, expiresAt := range c.used {
		if !now.Before(expiresAt) {
			delete(c.used, key)
		}
	}
}

// mac signe ce qui précède le MAC dans le défi, lié à la machine : le défi
// d'une machine ne sert pas pour une autre. L'identifiant est précédé de sa
// longueur pour que deux découpages différents ne donnent pas les mêmes
// octets.
func (c *challenges) mac(machineID string, signed []byte) []byte {
	mac := hmac.New(sha256.New, c.key)
	mac.Write([]byte(challengeLabel))
	mac.Write(binary.BigEndian.AppendUint32(nil, uint32(len(machineID)))) // #nosec G115 -- un identifiant tient en quelques dizaines d'octets.
	mac.Write([]byte(machineID))
	mac.Write(signed)
	return mac.Sum(nil)[:challengeMACSize]
}

func expiryOf(challenge []byte) time.Time {
	seconds := binary.BigEndian.Uint64(challenge[challengeRandomSize : challengeRandomSize+challengeExpirySize])
	return time.Unix(int64(seconds), 0) // #nosec G115 -- lu après le MAC : c'est la valeur que ce serveur a signée.
}
