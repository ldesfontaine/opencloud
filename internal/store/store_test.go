package store

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	db, err := Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func newKey(t *testing.T) []byte {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return public
}

func issueToken(t *testing.T, db *DB, name, machineID string, now time.Time) string {
	t.Helper()
	cleartext, token, err := machine.NewToken(name, machineID)
	if err != nil {
		t.Fatal(err)
	}
	token.CreatedAt = now
	token.ExpiresAt = now.Add(time.Hour)
	if err := db.InsertToken(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	return cleartext
}

func candidate(t *testing.T) machine.Machine {
	t.Helper()
	id, err := machine.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return machine.Machine{ID: id, Kind: machine.KindRemote, PublicKey: newKey(t), Hostname: "vps", OS: "Debian 12", Arch: "amd64"}
}

func TestOpen_AppliesMigrationsOnceAndSurvivesReopen(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	ctx := context.Background()
	db, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	versions, err := db.AppliedVersions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) == 0 || versions[0] != 1 {
		t.Fatalf("applied %v", versions)
	}
	db.Close()

	again, err := Open(ctx, root)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer again.Close()
	if _, err := again.ListMachines(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSaveLocalMachine_IsIdempotentAndListedFirst(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	local := machine.Machine{ID: machine.LocalID, Name: "opencloud", Kind: machine.KindLocal, OS: "Debian 12", EnrolledAt: now, LastSeenAt: now, CreatedAt: now}
	if err := db.SaveLocalMachine(ctx, local); err != nil {
		t.Fatal(err)
	}
	local.OS = "Debian 13"
	local.CreatedAt = now.Add(time.Hour)
	if err := db.SaveLocalMachine(ctx, local); err != nil {
		t.Fatal(err)
	}
	token := issueToken(t, db, "aaa-first", "", now)
	if _, err := db.Enroll(ctx, machine.HashToken(token), candidate(t), now); err != nil {
		t.Fatal(err)
	}
	machines, err := db.ListMachines(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(machines) != 2 || machines[0].ID != machine.LocalID || machines[0].OS != "Debian 13" {
		t.Fatalf("got %+v", machines)
	}
	if !machines[0].CreatedAt.Equal(now) {
		t.Fatalf("created_at moved to %v", machines[0].CreatedAt)
	}
}

func TestEnroll_ConsumesTheTokenAndCreatesTheMachine(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	token := issueToken(t, db, "vps-paris-1", "", now)
	wanted := candidate(t)

	enrolled, err := db.Enroll(ctx, machine.HashToken(token), wanted, now)
	if err != nil {
		t.Fatal(err)
	}
	if enrolled.ID != wanted.ID || enrolled.Name != "vps-paris-1" || enrolled.Kind != machine.KindRemote {
		t.Fatalf("got %+v", enrolled)
	}
	pending, err := db.ListPendingTokens(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("token still pending: %+v", pending)
	}
	if _, err := db.Enroll(ctx, machine.HashToken(token), candidate(t), now); !errors.Is(err, machine.ErrTokenConsumed) {
		t.Fatalf("second use: %v", err)
	}
}

func TestEnroll_ClassifiesEveryFailure(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)

	if _, err := db.Enroll(ctx, machine.HashToken("oc_nope"), candidate(t), now); !errors.Is(err, machine.ErrTokenNotFound) {
		t.Errorf("unknown: %v", err)
	}
	expired := issueToken(t, db, "old", "", now.Add(-2*time.Hour))
	if _, err := db.Enroll(ctx, machine.HashToken(expired), candidate(t), now); !errors.Is(err, machine.ErrTokenExpired) {
		t.Errorf("expired: %v", err)
	}
	first := issueToken(t, db, "same-name", "", now)
	if _, err := db.Enroll(ctx, machine.HashToken(first), candidate(t), now); err != nil {
		t.Fatal(err)
	}
	second := issueToken(t, db, "same-name", "", now)
	if _, err := db.Enroll(ctx, machine.HashToken(second), candidate(t), now); !errors.Is(err, machine.ErrNameTaken) {
		t.Errorf("name taken: %v", err)
	}
	// Le jeton refusé pour un nom pris n'est pas consommé : l'opérateur peut
	// retirer l'ancienne machine et réessayer.
	pending, err := db.ListPendingTokens(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending %+v", pending)
	}
}

func TestEnroll_SameTokenConcurrently_OnlyOneWins(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	token := issueToken(t, db, "raced", "", now)

	const racers = 8
	var wins int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := db.Enroll(ctx, machine.HashToken(token), candidate(t), now); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d enrollments succeeded, want 1", wins)
	}
}

func TestEnroll_WithMachineID_ReplacesTheKeyAndKeepsTheID(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	first := issueToken(t, db, "vps-lyon-2", "", now)
	original, err := db.Enroll(ctx, machine.HashToken(first), candidate(t), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.TouchMachine(ctx, original.ID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	again := issueToken(t, db, "vps-lyon-2", original.ID, now)
	replacement := candidate(t)
	reenrolled, err := db.Enroll(ctx, machine.HashToken(again), replacement, now.Add(30*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if reenrolled.ID != original.ID || string(reenrolled.PublicKey) != string(replacement.PublicKey) {
		t.Fatalf("got %+v", reenrolled)
	}
	if !reenrolled.LastSeenAt.IsZero() || !reenrolled.CreatedAt.Equal(original.CreatedAt) {
		t.Fatalf("history not reset as expected: %+v", reenrolled)
	}
	if _, err := db.GetMachine(ctx, replacement.ID); !errors.Is(err, machine.ErrNotFound) {
		t.Fatalf("the agent's new id must not become a machine: %v", err)
	}
}

func TestDeleteMachine_CascadesItsTokensAndSparesTheLocalOne(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	if err := db.SaveLocalMachine(ctx, machine.Machine{ID: machine.LocalID, Name: "opencloud", Kind: machine.KindLocal, EnrolledAt: now, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteMachine(ctx, machine.LocalID); !errors.Is(err, machine.ErrNotFound) {
		t.Fatalf("local delete: %v", err)
	}
	token := issueToken(t, db, "gone", "", now)
	enrolled, err := db.Enroll(ctx, machine.HashToken(token), candidate(t), now)
	if err != nil {
		t.Fatal(err)
	}
	issueToken(t, db, "gone", enrolled.ID, now)
	if err := db.DeleteMachine(ctx, enrolled.ID); err != nil {
		t.Fatal(err)
	}
	pending, err := db.ListPendingTokens(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("re-enroll token survived its machine: %+v", pending)
	}
}

func TestDeleteToken_OnlyWhilePending(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	cleartext := issueToken(t, db, "cancel-me", "", now)
	pending, err := db.ListPendingTokens(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteToken(ctx, pending[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteToken(ctx, pending[0].ID); !errors.Is(err, machine.ErrTokenNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if _, err := db.Enroll(ctx, machine.HashToken(cleartext), candidate(t), now); !errors.Is(err, machine.ErrTokenNotFound) {
		t.Fatalf("cancelled token still enrolls: %v", err)
	}
}

func TestRecordConnection_UpdatesAddressVersionAndLastSeen(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	token := issueToken(t, db, "seen", "", now)
	enrolled, err := db.Enroll(ctx, machine.HashToken(token), candidate(t), now)
	if err != nil {
		t.Fatal(err)
	}
	later := now.Add(time.Minute)
	if err := db.RecordConnection(ctx, enrolled.ID, "51.15.20.114", "v0.0.1", later); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetMachine(ctx, enrolled.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Address != "51.15.20.114" || got.AgentVersion != "v0.0.1" || !got.LastSeenAt.Equal(later) {
		t.Fatalf("got %+v", got)
	}
	if err := db.RecordConnection(ctx, "nobody", "", "", later); !errors.Is(err, machine.ErrNotFound) {
		t.Fatalf("unknown machine: %v", err)
	}
}
