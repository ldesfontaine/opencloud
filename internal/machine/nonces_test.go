package machine

import (
	"testing"
	"time"
)

var noncesNow = time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)

// Qui réclame des défis en boucle pour une machine connue ne fait que
// remplacer les siens : la table ne grossit pas.
func TestNonces_AMachineKeepsOnlyAFewPendingChallenges(t *testing.T) {
	pending := newNonces()
	var first []byte
	for i := range 1000 {
		nonce, err := pending.issue("machine-a", noncesNow)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = nonce
		}
	}
	if got := pending.size(); got != noncesPerMachine {
		t.Fatalf("%d pending challenges, want %d", got, noncesPerMachine)
	}
	if pending.consume(first, "machine-a", noncesNow) {
		t.Fatal("the oldest challenge was replaced and must not be accepted")
	}
	latest, err := pending.issue("machine-a", noncesNow)
	if err != nil {
		t.Fatal(err)
	}
	if !pending.consume(latest, "machine-a", noncesNow) {
		t.Fatal("the latest challenge must still be accepted")
	}
}

// Les défis expirés partent au balayage suivant, et leur machine avec.
func TestNonces_ExpiredChallengesAreSweptAway(t *testing.T) {
	pending := newNonces()
	for _, machineID := range []string{"machine-a", "machine-b", "machine-c"} {
		if _, err := pending.issue(machineID, noncesNow); err != nil {
			t.Fatal(err)
		}
	}
	later := noncesNow.Add(nonceLifetime + nonceSweepInterval)
	if _, err := pending.issue("machine-d", later); err != nil {
		t.Fatal(err)
	}
	if got := pending.size(); got != 1 {
		t.Fatalf("%d pending challenges after the sweep, want 1", got)
	}
	if len(pending.byMachine) != 1 {
		t.Fatalf("machines still indexed: %v", pending.byMachine)
	}
}
