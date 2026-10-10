package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/mcp"
)

// La migration 017 met en write les jetons d'API déjà créés : qui s'en
// sert ne perd pas ses écritures. On rejoue la migration sur une base où
// la colonne n'existe pas encore.
func TestMigration017_ExistingAPITokensKeepWriting(t *testing.T) {
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
	for _, statement := range []string{
		`ALTER TABLE mcp_tokens DROP COLUMN scope`,
		`DELETE FROM schema_migrations WHERE version = 17`,
		`INSERT INTO mcp_tokens (id, token_hash, token_prefix, kind, name, created_at) VALUES ('old', 'h-old', 'ock_old', 'api', 'ancien', 1)`,
	} {
		if _, err := db.sql.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	db.Close()

	again, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	token, err := again.GetMCPToken(ctx, "h-old")
	if err != nil || token.Scope != mcp.ScopeWrite || !token.ExpiresAt.IsZero() {
		t.Fatalf("%+v %v", token, err)
	}
}

func TestMCPToken_ScopeRoundTripsAndRevokeAllCounts(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	tokens := []mcp.Token{
		{ID: "r", Hash: "h-r", Prefix: "p", Kind: mcp.KindAPI, Scope: mcp.ScopeRead, CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
		{ID: "w", Hash: "h-w", Prefix: "p", Kind: mcp.KindAPI, Scope: mcp.ScopeWrite, CreatedAt: now},
		{ID: "old", Hash: "h-old", Prefix: "p", Kind: mcp.KindAPI, Scope: mcp.ScopeRead, CreatedAt: now, ExpiresAt: now.Add(-time.Hour)},
		{ID: "a", Hash: "h-a", Prefix: "p", Kind: mcp.KindAccess, Scope: mcp.ScopeWrite, FamilyID: "f1", CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
		{ID: "f", Hash: "h-f", Prefix: "p", Kind: mcp.KindRefresh, Scope: mcp.ScopeWrite, FamilyID: "f1", CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
	}
	for _, token := range tokens {
		if err := db.InsertMCPToken(ctx, token); err != nil {
			t.Fatal(err)
		}
	}
	if read, _ := db.GetMCPToken(ctx, "h-r"); read.Scope != mcp.ScopeRead {
		t.Fatalf("scope %+v", read)
	}
	revoked, err := db.RevokeAllMCPTokens(ctx, now)
	if err != nil || revoked != (mcp.Revoked{APITokens: 2, Sessions: 1}) {
		t.Fatalf("%+v %v", revoked, err)
	}
	for _, token := range tokens {
		got, err := db.GetMCPToken(ctx, token.Hash)
		if err != nil || !got.IsRevoked() {
			t.Fatalf("%s not revoked: %+v %v", token.ID, got, err)
		}
	}
}
