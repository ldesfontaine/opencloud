package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/verbrequest"
)

// La 025 s'ajoute à une base qui a déjà ses jetons et ses machines : rien de
// ce qui existe ne change, la table des demandes est vide, et un jeton révoqué
// emporte ses demandes.
func TestMigration025_AddsVerbRequestsToAnExistingBase(t *testing.T) {
	ctx := context.Background()
	db := openEmpty(t)
	names, err := migrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if version := migrationVersion(name); version < 25 {
			if err := db.apply(ctx, name, version); err != nil {
				t.Fatal(err)
			}
		}
	}
	exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES ('m1', 'nas', 1)`)
	exec(t, db, `INSERT INTO api_tokens (id, token_hash, token_prefix, scope, name, created_at) VALUES ('tok', 'h', 'p', 'write', 'agent', 1)`)
	exec(t, db, `INSERT INTO api_token_verbs (token_id, verb) VALUES ('tok', 'probe.pause')`)

	if err := db.apply(ctx, "025_verb_requests.sql", 25); err != nil {
		t.Fatal(err)
	}

	if tokens, machines := count(t, db, `SELECT COUNT(*) FROM api_tokens`), count(t, db, `SELECT COUNT(*) FROM machines`); tokens != 1 || machines != 1 {
		t.Fatalf("la migration a touché l'existant : %d jetons, %d machines", tokens, machines)
	}
	if requests := count(t, db, `SELECT COUNT(*) FROM verb_requests`); requests != 0 {
		t.Fatalf("%d demandes sur une base qui n'en avait pas", requests)
	}
	request := verbrequest.Request{
		ID: "r1", Verb: "machine.delete", ObjectName: "nas", Params: map[string]string{"id": "m1"},
		TokenID: "tok", TokenName: "agent", OnBehalfOf: "alice", State: verbrequest.StateAwaiting,
		CreatedAt: time.Unix(100, 0), ExpiresAt: time.Unix(1000, 0),
	}
	if err := db.InsertVerbRequest(ctx, request); err != nil {
		t.Fatal(err)
	}
	for _, state := range []verbrequest.State{verbrequest.StateAwaiting, verbrequest.StateConfirmed, verbrequest.StateRejected, verbrequest.StateExpired} {
		exec(t, db, `UPDATE verb_requests SET state = ? WHERE id = 'r1'`, string(state))
	}
	if _, err := db.sql.Exec(`UPDATE verb_requests SET state = 'perdue' WHERE id = 'r1'`); err == nil {
		t.Fatal("un état hors liste est accepté")
	}
	exec(t, db, `DELETE FROM api_tokens WHERE id = 'tok'`)
	if requests := count(t, db, `SELECT COUNT(*) FROM verb_requests`); requests != 0 {
		t.Fatal("révoquer le jeton a laissé sa demande")
	}
}

func insertToken(t *testing.T, db *DB, id string) {
	t.Helper()
	exec(t, db, `INSERT INTO api_tokens (id, token_hash, token_prefix, scope, name, created_at) VALUES (?, ?, 'p', 'write', ?, 1)`, id, id, id)
}

func awaiting(id, tokenID string, expiresAt int64) verbrequest.Request {
	return verbrequest.Request{
		ID: id, Verb: "machine.delete", ObjectName: "nas", Params: map[string]string{"id": "m1"}, Body: "",
		TokenID: tokenID, TokenName: tokenID, State: verbrequest.StateAwaiting,
		CreatedAt: time.Unix(100, 0), ExpiresAt: time.Unix(expiresAt, 0),
	}
}

// Décider se fait d'un seul geste : la seconde décision, la demande échue et
// la demande inconnue ne gagnent rien.
func TestVerbRequests_DecideOnlyOnceAndNeverAfterTheDeadline(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	insertToken(t, db, "tok")
	for _, request := range []verbrequest.Request{awaiting("a", "tok", 1000), awaiting("b", "tok", 1000), awaiting("late", "tok", 150)} {
		if err := db.InsertVerbRequest(ctx, request); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Unix(200, 0)

	decided, err := db.DecideVerbRequest(ctx, "a", verbrequest.StateConfirmed, now)
	if err != nil || decided.State != verbrequest.StateConfirmed || decided.Params["id"] != "m1" {
		t.Fatalf("première décision : %+v %v", decided, err)
	}
	if _, err := db.DecideVerbRequest(ctx, "a", verbrequest.StateRejected, now); !errors.Is(err, verbrequest.ErrNotFound) {
		t.Fatalf("seconde décision : %v", err)
	}
	if _, err := db.DecideVerbRequest(ctx, "late", verbrequest.StateConfirmed, now); !errors.Is(err, verbrequest.ErrNotFound) {
		t.Fatalf("demande échue : %v", err)
	}
	if _, err := db.DecideVerbRequest(ctx, "inconnue", verbrequest.StateConfirmed, now); !errors.Is(err, verbrequest.ErrNotFound) {
		t.Fatalf("demande inconnue : %v", err)
	}
	pending, err := db.ListPendingVerbRequests(ctx, now)
	if err != nil || len(pending) != 1 || pending[0].ID != "b" {
		t.Fatalf("en attente : %+v %v", pending, err)
	}
}

func TestVerbRequests_ExpireAndPurgeOnlyWhatIsDue(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	insertToken(t, db, "tok")
	for _, request := range []verbrequest.Request{awaiting("old", "tok", 150), awaiting("fresh", "tok", 1000)} {
		if err := db.InsertVerbRequest(ctx, request); err != nil {
			t.Fatal(err)
		}
	}

	expired, err := db.ExpireDueVerbRequests(ctx, time.Unix(200, 0))
	if err != nil || len(expired) != 1 || expired[0].ID != "old" {
		t.Fatalf("échues : %+v %v", expired, err)
	}
	if again, err := db.ExpireDueVerbRequests(ctx, time.Unix(200, 0)); err != nil || len(again) != 0 {
		t.Fatalf("une demande échoit une seule fois : %+v %v", again, err)
	}
	got, err := db.GetVerbRequest(ctx, "old")
	if err != nil || got.State != verbrequest.StateExpired {
		t.Fatalf("état : %+v %v", got, err)
	}
	if err := db.PurgeVerbRequests(ctx, time.Unix(900, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetVerbRequest(ctx, "old"); !errors.Is(err, verbrequest.ErrNotFound) {
		t.Fatalf("la demande terminée et ancienne reste : %v", err)
	}
	if _, err := db.GetVerbRequest(ctx, "fresh"); err != nil {
		t.Fatalf("la demande en attente a été purgée : %v", err)
	}
}
