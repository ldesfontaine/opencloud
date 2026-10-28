package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/apitoken"
)

// La 008 garde les jetons d'API vivants, en lecture, avec leur identifiant,
// leur empreinte, leur échéance et leur dernier usage ; elle laisse tomber le
// client d'autorisation, ses codes, les jetons d'accès et de rafraîchissement,
// et les jetons d'API révoqués.
func TestMigration008_KeepsLiveAPITokensReadOnlyAndDropsTheRest(t *testing.T) {
	ctx := context.Background()
	db := openEmpty(t)
	names, err := migrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if version := migrationVersion(name); version < 8 {
			if err := db.apply(ctx, name, version); err != nil {
				t.Fatal(err)
			}
		}
	}

	seed, err := os.ReadFile("testdata/legacy/before_008.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec(t, db, string(seed))

	if err := db.apply(ctx, "008_api_tokens.sql", 8); err != nil {
		t.Fatal(err)
	}

	if rows := count(t, db, `SELECT COUNT(*) FROM api_tokens`); rows != 2 {
		t.Fatalf("api_tokens: %d lignes, 2 attendues", rows)
	}
	if rows := count(t, db, `SELECT COUNT(*) FROM api_tokens WHERE scope = 'read'`); rows != 2 {
		t.Fatalf("un jeton write d'avant lit désormais : %d lignes en read sur 2", rows)
	}
	if got := text(t, db, `SELECT name FROM api_tokens WHERE id = 'tok_read'`); got != "mcp-maison" {
		t.Fatalf("nom %q", got)
	}
	if got := text(t, db, `SELECT token_hash FROM api_tokens WHERE id = 'tok_read'`); got != "hash_read" {
		t.Fatalf("empreinte %q : le jeton que mcp-maison porte déjà doit continuer de servir", got)
	}
	if expires, used := count(t, db, `SELECT expires_at FROM api_tokens WHERE id = 'tok_read'`), count(t, db, `SELECT last_used_at FROM api_tokens WHERE id = 'tok_read'`); expires != 5000 || used != 4000 {
		t.Fatalf("échéance %d, dernier usage %d", expires, used)
	}
	if rows := count(t, db, `SELECT COUNT(*) FROM api_tokens WHERE id IN ('tok_revoked', 'tok_access', 'tok_refresh')`); rows != 0 {
		t.Fatalf("%d jeton(s) qui ne devaient pas survivre", rows)
	}
	if tables := count(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE name LIKE 'mcp\_%' ESCAPE '\'`); tables != 0 {
		t.Fatalf("%d objet(s) mcp_ restent dans le schéma", tables)
	}
	// Une ligne neuve sans portée lit.
	exec(t, db, `INSERT INTO api_tokens (id, token_hash, token_prefix, name, created_at) VALUES ('neuf', 'h', 'p', 'n', 1)`)
	if got := text(t, db, `SELECT scope FROM api_tokens WHERE id = 'neuf'`); got != "read" {
		t.Fatalf("portée par défaut %q", got)
	}
	if _, err := db.sql.Exec(`INSERT INTO api_tokens (id, token_hash, token_prefix, scope, name, created_at) VALUES ('x', 'h2', 'p', 'admin', 'n', 1)`); err == nil {
		t.Fatal("une portée inconnue est acceptée")
	}
}

func newAPIToken(id, hash string, expires time.Time) apitoken.Token {
	return apitoken.Token{ID: id, Hash: hash, Prefix: "ock_" + id, Scope: apitoken.ScopeRead, Name: "jeton " + id, CreatedAt: time.Unix(1000, 0), ExpiresAt: expires}
}

func TestAPITokens_InsertGetListTouchDelete(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	soon := time.Unix(5000, 0)
	if err := db.InsertAPIToken(ctx, newAPIToken("a", "ha", soon)); err != nil {
		t.Fatal(err)
	}
	later := newAPIToken("b", "hb", time.Time{})
	later.CreatedAt = time.Unix(2000, 0)
	if err := db.InsertAPIToken(ctx, later); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertAPIToken(ctx, newAPIToken("c", "ha", soon)); err == nil {
		t.Fatal("deux jetons de même empreinte")
	}

	got, err := db.GetAPIToken(ctx, "ha")
	if err != nil || got.ID != "a" || !got.ExpiresAt.Equal(soon) || !got.LastUsedAt.IsZero() || got.Scope != apitoken.ScopeRead {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := db.GetAPIToken(ctx, "inconnu"); !errors.Is(err, apitoken.ErrNotFound) {
		t.Fatalf("get unknown: %v", err)
	}
	listed, err := db.ListAPITokens(ctx)
	if err != nil || len(listed) != 2 || listed[0].ID != "b" || !listed[0].ExpiresAt.IsZero() {
		t.Fatalf("list: %+v %v", listed, err)
	}

	used := time.Unix(3000, 0)
	if err := db.TouchAPIToken(ctx, "a", used); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.GetAPIToken(ctx, "ha"); !got.LastUsedAt.Equal(used) {
		t.Fatalf("last used %v", got.LastUsedAt)
	}
	if err := db.DeleteAPIToken(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteAPIToken(ctx, "a"); !errors.Is(err, apitoken.ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
	if _, err := db.GetAPIToken(ctx, "ha"); !errors.Is(err, apitoken.ErrNotFound) {
		t.Fatalf("a revoked token is still there: %v", err)
	}
}

// DeleteAll efface tout, mais ne compte que les jetons qui servaient encore.
func TestAPITokens_DeleteAllCountsOnlyTheLiveOnes(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	now := time.Unix(10_000, 0)
	for _, token := range []apitoken.Token{
		newAPIToken("live", "h1", now.Add(time.Hour)),
		newAPIToken("forever", "h2", time.Time{}),
		newAPIToken("expired", "h3", now.Add(-time.Hour)),
	} {
		if err := db.InsertAPIToken(ctx, token); err != nil {
			t.Fatal(err)
		}
	}
	revoked, err := db.DeleteAllAPITokens(ctx, now)
	if err != nil || revoked != 2 {
		t.Fatalf("revoked %d, %v", revoked, err)
	}
	if listed, _ := db.ListAPITokens(ctx); len(listed) != 0 {
		t.Fatalf("left: %+v", listed)
	}
}

func TestAPITokens_PurgeRemovesOnlyTheExpired(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	now := time.Unix(10_000, 0)
	for _, token := range []apitoken.Token{
		newAPIToken("live", "h1", now.Add(time.Hour)),
		newAPIToken("forever", "h2", time.Time{}),
		newAPIToken("expired", "h3", now.Add(-time.Hour)),
	} {
		if err := db.InsertAPIToken(ctx, token); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.PurgeExpiredAPITokens(ctx, now); err != nil {
		t.Fatal(err)
	}
	listed, _ := db.ListAPITokens(ctx)
	if len(listed) != 2 {
		t.Fatalf("left: %+v", listed)
	}
	for _, token := range listed {
		if token.ID == "expired" {
			t.Fatal("an expired token survived the purge")
		}
	}
}

// Les verbes d'un jeton vivent et meurent avec lui : le revoquer les emporte,
// et ils reviennent triés à la lecture.
func TestAPITokenVerbs_RoundTripAndCascade(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	token := apitoken.Token{
		ID: "tok_w", Hash: "hash_w", Prefix: "ock_w", Scope: apitoken.ScopeWrite, Name: "agent",
		CreatedAt: time.Unix(1000, 0), Verbs: []string{"probe.pause", "agent.ping"},
	}
	if err := db.InsertAPIToken(ctx, token); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetAPIToken(ctx, "hash_w")
	if err != nil || len(got.Verbs) != 2 || got.Verbs[0] != "agent.ping" || got.Verbs[1] != "probe.pause" {
		t.Fatalf("get: %+v %v", got, err)
	}
	if err := db.DeleteAPIToken(ctx, "tok_w"); err != nil {
		t.Fatal(err)
	}
	if rows := count(t, db, `SELECT COUNT(*) FROM api_token_verbs`); rows != 0 {
		t.Fatalf("%d verbes restent après la révocation", rows)
	}
}

// Un jeton refusé à l'insertion n'en laisse pas la moitié.
func TestAPITokenVerbs_InsertIsAllOrNothing(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	token := apitoken.Token{
		ID: "tok_w", Hash: "hash_w", Prefix: "ock_w", Scope: apitoken.ScopeWrite, Name: "agent",
		CreatedAt: time.Unix(1000, 0), Verbs: []string{"agent.ping", "agent.ping"},
	}
	if err := db.InsertAPIToken(ctx, token); err == nil {
		t.Fatal("a duplicated verb was accepted")
	}
	if rows := count(t, db, `SELECT COUNT(*) FROM api_tokens`); rows != 0 {
		t.Fatalf("%d jeton(s) à moitié écrit(s)", rows)
	}
}
