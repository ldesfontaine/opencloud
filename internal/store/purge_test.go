package store

import (
	"context"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/update"
)

var purgeNow = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

// rowIDs lit une colonne d'identifiants dans l'ordre : la purge s'observe
// sur la table, pas sur une lecture qui filtre déjà.
func rowIDs(t *testing.T, db *DB, query string) []string {
	t.Helper()
	rows, err := db.sql.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	items, err := collect(rows, func(row scanner) (string, error) {
		var id string
		err := row.Scan(&id)
		return id, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func requireRowIDs(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestPurgeExpiredTokens_DropsTheExpiredUnusedAndKeepsTheValid(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	for _, token := range []struct {
		name    string
		expires time.Time
	}{
		{"expired", purgeNow.Add(-time.Minute)},
		{"valid", purgeNow.Add(time.Hour)},
	} {
		_, row, err := machine.NewToken(token.name, "")
		if err != nil {
			t.Fatal(err)
		}
		row.ID, row.CreatedAt, row.ExpiresAt = token.name, purgeNow.Add(-2*time.Hour), token.expires
		if err := db.InsertToken(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.PurgeTokens(ctx, purgeNow, purgeNow.Add(-machine.ConsumedTokenRetention)); err != nil {
		t.Fatal(err)
	}
	requireRowIDs(t, rowIDs(t, db, `SELECT id FROM enrollment_tokens ORDER BY id`), "valid")
	pending, err := db.ListPendingTokens(ctx, purgeNow)
	if err != nil || len(pending) != 1 || pending[0].ID != "valid" {
		t.Fatalf("pending %+v, %v: the valid token must still be usable", pending, err)
	}
}

func TestPurgeImageChecks_DropsTheOlderChecksAndKeepsTheOthers(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	insertProbeOnLocalMachine(t, db)
	cutoff := purgeNow.Add(-30 * 24 * time.Hour)
	checks := []update.Check{
		{MachineID: machine.LocalID, Image: "old:1", CheckedAt: cutoff.Add(-time.Second), Outcome: update.OutcomeOK},
		{MachineID: machine.LocalID, Image: "limit:1", CheckedAt: cutoff, Outcome: update.OutcomeOK},
		{MachineID: machine.LocalID, Image: "recent:1", CheckedAt: purgeNow, Outcome: update.OutcomeOK},
	}
	if err := db.ApplyImageChecks(ctx, checks); err != nil {
		t.Fatal(err)
	}
	if err := db.PurgeImageChecks(ctx, cutoff); err != nil {
		t.Fatal(err)
	}
	remaining, err := db.ListImageChecks(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	var images []string
	for _, check := range remaining {
		images = append(images, check.Image)
	}
	// La borne est stricte : un constat daté de l'instant même reste.
	requireRowIDs(t, images, "limit:1", "recent:1")
}
