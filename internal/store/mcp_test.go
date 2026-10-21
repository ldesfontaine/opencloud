package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/mcp"
)

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

// Un code d'autorisation en attente se changerait encore en jetons : la
// révocation générale l'efface avec eux, et la régénération du secret
// efface ceux de l'ancien client.
func TestMCPCodes_RevokeAllAndRevokeOAuthDropThePendingOnes(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	pending := mcp.Code{Hash: "h-code", ClientID: mcp.ClientID, RedirectURI: "http://localhost:1/cb", Challenge: "c", Scope: mcp.ScopeRead, ExpiresAt: now.Add(mcp.CodeTTL), CreatedAt: now}
	cases := map[string]func(*DB, context.Context) error{
		"revoke all": func(db *DB, ctx context.Context) error {
			_, err := db.RevokeAllMCPTokens(ctx, now)
			return err
		},
		"revoke oauth": func(db *DB, ctx context.Context) error {
			return db.RevokeMCPOAuthTokens(ctx, now)
		},
	}
	for name, revoke := range cases {
		t.Run(name, func(t *testing.T) {
			db := openTestDB(t)
			ctx := context.Background()
			if err := db.InsertMCPCode(ctx, pending); err != nil {
				t.Fatal(err)
			}
			if err := revoke(db, ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ConsumeMCPCode(ctx, pending.Hash, now); !errors.Is(err, mcp.ErrCodeInvalid) {
				t.Fatalf("a pending code survived the revocation: %v", err)
			}
		})
	}
}
