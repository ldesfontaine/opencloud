package machine

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var challengesNow = time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)

const (
	machineA = "01234567-89ab-cdef-0123-456789abcdef"
	machineB = "76543210-89ab-cdef-0123-456789abcdef"
)

// size compte les défis utilisés encore retenus.
func (c *challenges) size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.used)
}

func TestChallenge_FitsTheSizeInstalledAgentsSign(t *testing.T) {
	challenge := newChallenges().issue(machineA, challengesNow)
	if len(challenge) != NonceSize {
		t.Fatalf("challenge is %d bytes, installed agents only sign %d", len(challenge), NonceSize)
	}
	if _, err := SignedPayload(challenge, machineA, 1); err != nil {
		t.Fatalf("the agent cannot sign a challenge: %v", err)
	}
}

// Rien n'est gardé à l'émission : demander des défis en boucle ne fait pas
// grossir le serveur, et n'ôte rien aux défis des autres.
func TestChallenge_IssuingKeepsNothingAndNeverEvictsAnother(t *testing.T) {
	pending := newChallenges()
	agents := pending.issue(machineA, challengesNow)
	for range 1000 {
		pending.issue(machineA, challengesNow)
	}
	if got := pending.size(); got != 0 {
		t.Fatalf("%d entries kept after issuing, want 0", got)
	}
	if !pending.valid(agents, machineA, challengesNow) {
		t.Fatal("the agent's challenge must outlive any number of anonymous ones")
	}
}

func TestChallenge_IsRefusedWhenExpiredForeignOrAltered(t *testing.T) {
	pending := newChallenges()
	challenge := pending.issue(machineA, challengesNow)

	if !pending.valid(challenge, machineA, challengesNow.Add(challengeLifetime-time.Second)) {
		t.Fatal("a fresh challenge must be accepted")
	}
	if pending.valid(challenge, machineA, challengesNow.Add(challengeLifetime+time.Second)) {
		t.Error("an expired challenge was accepted")
	}
	if pending.valid(challenge, machineB, challengesNow) {
		t.Error("a challenge issued for another machine was accepted")
	}
	for i := range challenge {
		altered := append([]byte(nil), challenge...)
		altered[i] ^= 0x01
		if pending.valid(altered, machineA, challengesNow) {
			t.Errorf("a challenge altered at byte %d was accepted", i)
		}
	}
	for _, wrong := range [][]byte{nil, challenge[:NonceSize-1], append(challenge[:NonceSize:NonceSize], 0)} {
		if pending.valid(wrong, machineA, challengesNow) {
			t.Errorf("a challenge of %d bytes was accepted", len(wrong))
		}
	}
}

// Le serveur qui redémarre tire une nouvelle clé : les défis d'avant ne
// valent plus rien, et l'agent le sait par challenge_expired.
func TestChallenge_DoesNotSurviveARestart(t *testing.T) {
	before := newChallenges().issue(machineA, challengesNow)
	if newChallenges().valid(before, machineA, challengesNow) {
		t.Fatal("a challenge issued before the restart was accepted")
	}
}

func TestChallenge_IsUsedOnlyOnce(t *testing.T) {
	pending := newChallenges()
	challenge := pending.issue(machineA, challengesNow)
	if !pending.markUsed(challenge, challengesNow) {
		t.Fatal("the first use must pass")
	}
	if pending.markUsed(challenge, challengesNow) {
		t.Fatal("the second use must be refused")
	}
}

// Deux preuves identiques en même temps : une seule passe.
func TestChallenge_ConcurrentUsesLetOnlyOnePass(t *testing.T) {
	pending := newChallenges()
	challenge := pending.issue(machineA, challengesNow)
	var passed atomic.Int32
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			if pending.markUsed(challenge, challengesNow) {
				passed.Add(1)
			}
		})
	}
	wg.Wait()
	if got := passed.Load(); got != 1 {
		t.Fatalf("%d uses passed, want 1", got)
	}
}

// Les défis utilisés et expirés partent au balayage suivant.
func TestChallenge_UsedOnesAreSweptOnceExpired(t *testing.T) {
	pending := newChallenges()
	for _, machineID := range []string{machineA, machineB} {
		if !pending.markUsed(pending.issue(machineID, challengesNow), challengesNow) {
			t.Fatal("first use refused")
		}
	}
	if got := pending.size(); got != 2 {
		t.Fatalf("%d used challenges, want 2", got)
	}
	later := challengesNow.Add(challengeLifetime + challengeSweepInterval)
	if !pending.markUsed(pending.issue(machineA, later), later) {
		t.Fatal("first use refused")
	}
	if got := pending.size(); got != 1 {
		t.Fatalf("%d used challenges after the sweep, want 1", got)
	}
}
