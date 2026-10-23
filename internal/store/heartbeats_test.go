package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/seal"
)

// Le jeton de ping n'a qu'une forme en base : chiffré dans token_sealed,
// cherché par token_lookup. Les deux s'écrivent ensemble, l'ancien jeton
// cesse de trouver la tâche, et deux tâches ne partagent jamais un jeton.
func TestHeartbeatToken_IsSealedAndFoundByItsLookup(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	job := heartbeat.Heartbeat{ID: "job1", Token: "hb_first", Name: "sauvegarde", Status: heartbeat.StatusNew, Interval: time.Hour, CreatedAt: time.Unix(1, 0)}
	if err := db.InsertHeartbeat(ctx, job); err != nil {
		t.Fatal(err)
	}
	if sealed := text(t, db, `SELECT token_sealed FROM heartbeats`); !seal.IsSealed(sealed) {
		t.Fatalf("jeton en clair : %q", sealed)
	}
	if lookup := text(t, db, `SELECT token_lookup FROM heartbeats`); lookup != heartbeat.HashToken("hb_first") {
		t.Fatalf("token_lookup = %q", lookup)
	}
	if found, err := db.GetHeartbeatByToken(ctx, "hb_first"); err != nil || found.ID != "job1" || found.Token != "hb_first" {
		t.Fatalf("par le jeton : %+v %v", found, err)
	}

	if err := db.SetHeartbeatToken(ctx, "job1", "hb_second"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetHeartbeatByToken(ctx, "hb_first"); !errors.Is(err, heartbeat.ErrNotFound) {
		t.Fatalf("l'ancien jeton trouve encore la tâche : %v", err)
	}
	if found, err := db.GetHeartbeat(ctx, "job1"); err != nil || found.Token != "hb_second" {
		t.Fatalf("relu : %+v %v", found, err)
	}

	twin := job
	twin.ID, twin.Token = "job2", "hb_second"
	if err := db.InsertHeartbeat(ctx, twin); err == nil {
		t.Fatal("deux tâches ne doivent pas partager un jeton")
	}
}
