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

// La 022 laisse les jetons d'avant « partout » : ni drapeau ni liste, leurs
// verbes intacts. Une ligne neuve sans portée l'est aussi, et un drapeau
// hors 0/1 est refusé.
func TestMigration022_KeepsExistingTokensEverywhere(t *testing.T) {
	ctx := context.Background()
	db := openEmpty(t)
	names, err := migrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if version := migrationVersion(name); version < 22 {
			if err := db.apply(ctx, name, version); err != nil {
				t.Fatal(err)
			}
		}
	}
	exec(t, db, `INSERT INTO api_tokens (id, token_hash, token_prefix, scope, name, created_at) VALUES ('avant', 'h', 'p', 'write', 'avant', 1)`)
	exec(t, db, `INSERT INTO api_token_verbs (token_id, verb) VALUES ('avant', 'probe.pause')`)

	if err := db.apply(ctx, "022_api_token_reach.sql", 22); err != nil {
		t.Fatal(err)
	}

	token, err := db.GetAPIToken(ctx, "h")
	if err != nil {
		t.Fatal(err)
	}
	if token.Reach.IsLimited() || len(token.Reach.Machines) != 0 || len(token.Reach.Guests) != 0 {
		t.Fatalf("un jeton d'avant est limité : %+v", token.Reach)
	}
	if len(token.Verbs) != 1 || token.Verbs[0] != "probe.pause" {
		t.Fatalf("verbes %v", token.Verbs)
	}
	for _, table := range []string{"api_token_machines", "api_token_guests"} {
		if rows := count(t, db, `SELECT COUNT(*) FROM `+table); rows != 0 {
			t.Fatalf("%s : %d ligne(s) sur une base d'avant", table, rows)
		}
	}
	if _, err := db.sql.Exec(`UPDATE api_tokens SET machines_limited = 2 WHERE id = 'avant'`); err == nil {
		t.Fatal("un drapeau hors 0 et 1 est accepté")
	}
	if _, err := db.sql.Exec(`INSERT INTO api_token_guests (token_id, vmid) VALUES ('avant', 0)`); err == nil {
		t.Fatal("un numéro d'invité nul est accepté")
	}
}

// La portée fait l'aller-retour de la base, triée ; une liste vide limitée
// reste limitée ; la remplacer efface l'ancienne ; supprimer une machine la
// retire de la liste sans élargir la portée ; révoquer le jeton emporte tout.
func TestAPITokens_ReachRoundTripAndCascades(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES ('m1', 'un', 1), ('m2', 'deux', 1)`)
	limited := newAPIToken("lim", "h-lim", time.Time{})
	limited.Scope = apitoken.ScopeWrite
	limited.Reach = apitoken.Reach{OnlyMachines: true, Machines: []string{"m1", "m2"}, OnlyGuests: true, Guests: []int{200, 5012}}
	empty := newAPIToken("vide", "h-vide", time.Time{})
	empty.Scope = apitoken.ScopeWrite
	empty.Reach = apitoken.Reach{OnlyMachines: true}
	free := newAPIToken("libre", "h-libre", time.Time{})
	free.Scope = apitoken.ScopeWrite
	for _, token := range []apitoken.Token{limited, empty, free} {
		if err := db.InsertAPIToken(ctx, token); err != nil {
			t.Fatal(err)
		}
	}

	got, err := db.GetAPIToken(ctx, "h-lim")
	if err != nil || !got.Reach.OnlyMachines || !got.Reach.OnlyGuests || len(got.Reach.Machines) != 2 || got.Reach.Machines[0] != "m1" || got.Reach.Guests[1] != 5012 {
		t.Fatalf("get: %+v %v", got.Reach, err)
	}
	listed, err := db.ListAPITokens(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]apitoken.Token{}
	for _, token := range listed {
		byID[token.ID] = token
	}
	if !byID["vide"].Reach.OnlyMachines || len(byID["vide"].Reach.Machines) != 0 {
		t.Fatalf("empty list: %+v", byID["vide"].Reach)
	}
	if byID["libre"].Reach.IsLimited() || len(byID["lim"].Reach.Machines) != 2 {
		t.Fatalf("list mixes the reaches: %+v %+v", byID["libre"].Reach, byID["lim"].Reach)
	}

	if err := db.UpdateAPITokenReach(ctx, "lim", apitoken.Reach{OnlyMachines: true, Machines: []string{"m2"}}); err != nil {
		t.Fatal(err)
	}
	got, _ = db.GetAPIToken(ctx, "h-lim")
	if got.Reach.OnlyGuests || len(got.Reach.Guests) != 0 || len(got.Reach.Machines) != 1 || got.Reach.Machines[0] != "m2" {
		t.Fatalf("after replace: %+v", got.Reach)
	}
	if err := db.UpdateAPITokenReach(ctx, "absent", apitoken.Reach{}); !errors.Is(err, apitoken.ErrNotFound) {
		t.Fatalf("unknown token: %v", err)
	}

	exec(t, db, `DELETE FROM machines WHERE id = 'm2'`)
	got, _ = db.GetAPIToken(ctx, "h-lim")
	if !got.Reach.OnlyMachines || len(got.Reach.Machines) != 0 {
		t.Fatalf("a deleted machine widened or kept the reach: %+v", got.Reach)
	}

	if err := db.DeleteAPIToken(ctx, "lim"); err != nil {
		t.Fatal(err)
	}
	if rows := count(t, db, `SELECT COUNT(*) FROM api_token_machines WHERE token_id = 'lim'`) + count(t, db, `SELECT COUNT(*) FROM api_token_guests WHERE token_id = 'lim'`); rows != 0 {
		t.Fatalf("%d ligne(s) de portée survivent à la révocation", rows)
	}
}
