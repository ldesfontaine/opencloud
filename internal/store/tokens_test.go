package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
)

// La purge efface les jetons périmés jamais servis et les consommés depuis
// plus que la rétention ; un jeton valable et un consommé récent restent.
func TestPurgeTokens_ExpiredPendingAndOldConsumedOnly(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	enrolledAt := func(name string, at time.Time) (string, machine.Machine) {
		token := issueToken(t, db, name, "", at)
		enrolled, err := db.Enroll(ctx, machine.HashToken(token), candidate(t), newAgent(t, at), at)
		if err != nil {
			t.Fatal(err)
		}
		return token, enrolled
	}
	oldConsumed, revoked := enrolledAt("old", now.Add(-machine.ConsumedTokenRetention-time.Hour))
	if _, err := db.RevokeAgent(ctx, revoked.ID, now); err != nil {
		t.Fatal(err)
	}
	// Le jeton d'un agent actif reste, si vieux soit-il : il dit ce qu'on
	// lui demandait.
	activeConsumed, _ := enrolledAt("active", now.Add(-machine.ConsumedTokenRetention-time.Hour))
	recentConsumed, _ := enrolledAt("recent", now.Add(-machine.ConsumedTokenRetention+time.Hour))
	expired := issueToken(t, db, "expired", "", now.Add(-2*time.Hour))
	issueToken(t, db, "pending", "", now)

	if err := db.PurgeTokens(ctx, now, now.Add(-machine.ConsumedTokenRetention)); err != nil {
		t.Fatal(err)
	}
	pending, err := db.ListPendingTokens(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Name != "pending" {
		t.Fatalf("pending after purge: %+v", pending)
	}
	cases := []struct {
		name  string
		token string
		want  error
	}{
		{"old consumed by a revoked agent", oldConsumed, machine.ErrTokenNotFound},
		{"old consumed by an active agent", activeConsumed, machine.ErrTokenConsumed},
		{"recent consumed", recentConsumed, machine.ErrTokenConsumed},
		{"expired pending", expired, machine.ErrTokenNotFound},
	}
	for _, c := range cases {
		if _, err := db.Enroll(ctx, machine.HashToken(c.token), candidate(t), newAgent(t, now), now); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
}
