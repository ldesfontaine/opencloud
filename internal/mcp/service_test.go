package mcp_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/mcp"
	"github.com/ldesfontaine/opencloud/internal/store"
)

var testNow = time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)

// Le service se teste sur la vraie base SQLite, temporaire, avec une
// horloge qu'on avance à la main.
type fixture struct {
	*mcp.Service
	db    *store.DB
	clock time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	db, err := store.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f := &fixture{db: db, clock: testNow}
	f.Service = mcp.New(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	f.SetClock(func() time.Time { return f.clock })
	return f
}

func (f *fixture) enable(t *testing.T) string {
	t.Helper()
	secret, _, err := f.Enable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

const (
	verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	callback = "http://127.0.0.1:33418/oauth/callback"
)

func challengeOf(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// authorize joue une autorisation valide et rend le code.
func (f *fixture) authorize(t *testing.T) string {
	t.Helper()
	code, err := f.Authorize(context.Background(), mcp.AuthorizeRequest{
		ResponseType: "code", ClientID: mcp.ClientID, RedirectURI: callback,
		Challenge: challengeOf(verifier), ChallengeMethod: "S256",
	})
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func (f *fixture) exchange(t *testing.T, secret, code string) mcp.Grant {
	t.Helper()
	grant, err := f.Exchange(context.Background(), mcp.ExchangeRequest{
		ClientID: mcp.ClientID, ClientSecret: secret, Code: code, RedirectURI: callback, Verifier: verifier,
	})
	if err != nil {
		t.Fatal(err)
	}
	return grant
}

func TestEnable_GivesTheSecretOnceAndRefusesTwice(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, enabled, _ := f.Client(ctx); enabled {
		t.Fatal("enabled before Enable")
	}
	secret, client, err := f.Enable(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, "ocs_") || client.ID != mcp.ClientID || !strings.HasPrefix(secret, client.SecretPrefix) {
		t.Fatalf("secret %q client %+v", secret, client)
	}
	if client.SecretHash == secret || strings.Contains(client.SecretHash, secret[4:]) {
		t.Fatal("the secret must only be stored hashed")
	}
	if _, _, err := f.Enable(ctx); !errors.Is(err, mcp.ErrAlreadyEnabled) {
		t.Fatalf("second enable: %v", err)
	}
	if _, enabled, _ := f.Client(ctx); !enabled {
		t.Fatal("not enabled after Enable")
	}
}

func TestDisable_RemovesTheClientAndEveryToken(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	secret := f.enable(t)
	grant := f.exchange(t, secret, f.authorize(t))
	apiToken, _, err := f.CreateAPIToken(ctx, "script")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Disable(ctx); err != nil {
		t.Fatal(err)
	}
	if _, enabled, _ := f.Client(ctx); enabled {
		t.Fatal("still enabled")
	}
	for _, bearer := range []string{grant.AccessToken, apiToken} {
		if _, err := f.Verify(ctx, bearer); !errors.Is(err, mcp.ErrDisabled) {
			t.Fatalf("verify after disable: %v", err)
		}
	}
	if err := f.Disable(ctx); !errors.Is(err, mcp.ErrNotFound) {
		t.Fatalf("second disable: %v", err)
	}
	// Réactivé, rien de l'ancien monde ne revient.
	f.enable(t)
	if _, err := f.Verify(ctx, apiToken); !errors.Is(err, mcp.ErrTokenInvalid) {
		t.Fatalf("old api token after re-enable: %v", err)
	}
}

func TestAPIToken_CreateVerifyRevoke(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, _, err := f.CreateAPIToken(ctx, "script"); !errors.Is(err, mcp.ErrDisabled) {
		t.Fatalf("create while disabled: %v", err)
	}
	f.enable(t)
	if _, _, err := f.CreateAPIToken(ctx, "  "); !errors.Is(err, mcp.ErrNameInvalid) {
		t.Fatalf("empty name: %v", err)
	}
	cleartext, token, err := f.CreateAPIToken(ctx, " sauvegarde ")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cleartext, "ock_") || token.Name != "sauvegarde" || token.Kind != mcp.KindAPI || !token.ExpiresAt.IsZero() {
		t.Fatalf("token %+v", token)
	}
	f.clock = f.clock.Add(time.Hour)
	verified, err := f.Verify(ctx, cleartext)
	if err != nil || verified.ID != token.ID || !verified.LastUsedAt.Equal(f.clock) {
		t.Fatalf("verify: %+v %v", verified, err)
	}
	listed, err := f.APITokens(ctx)
	if err != nil || len(listed) != 1 || listed[0].LastUsedAt.IsZero() {
		t.Fatalf("list: %+v %v", listed, err)
	}
	if _, err := f.Verify(ctx, "ock_"+strings.Repeat("a", 52)); !errors.Is(err, mcp.ErrTokenInvalid) {
		t.Fatalf("unknown token: %v", err)
	}
	if err := f.RevokeToken(ctx, token.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Verify(ctx, cleartext); !errors.Is(err, mcp.ErrTokenInvalid) {
		t.Fatalf("revoked token: %v", err)
	}
	if err := f.RevokeToken(ctx, token.ID); !errors.Is(err, mcp.ErrNotFound) {
		t.Fatalf("revoke twice: %v", err)
	}
	if listed, _ := f.APITokens(ctx); len(listed) != 0 {
		t.Fatalf("revoked token still listed: %+v", listed)
	}
	for range mcp.MaxAPITokens {
		if _, _, err := f.CreateAPIToken(ctx, "n"); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := f.CreateAPIToken(ctx, "one too many"); !errors.Is(err, mcp.ErrTooManyTokens) {
		t.Fatalf("cap: %v", err)
	}
}

func TestAuthorize_RefusesEverythingButAValidRequest(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	valid := mcp.AuthorizeRequest{ResponseType: "code", ClientID: mcp.ClientID, RedirectURI: callback, Challenge: challengeOf(verifier), ChallengeMethod: "S256"}
	if _, err := f.Authorize(ctx, valid); !errors.Is(err, mcp.ErrDisabled) {
		t.Fatalf("disabled: %v", err)
	}
	f.enable(t)
	cases := map[string]struct {
		change func(*mcp.AuthorizeRequest)
		want   error
	}{
		"remote redirect not declared": {func(r *mcp.AuthorizeRequest) { r.RedirectURI = "https://claude.ai/api/mcp/auth_callback" }, mcp.ErrRedirectURIInvalid},
		"relative redirect":            {func(r *mcp.AuthorizeRequest) { r.RedirectURI = "/callback" }, mcp.ErrRedirectURIInvalid},
		"response type":                {func(r *mcp.AuthorizeRequest) { r.ResponseType = "token" }, mcp.ErrResponseTypeInvalid},
		"client":                       {func(r *mcp.AuthorizeRequest) { r.ClientID = "other" }, mcp.ErrClientUnknown},
		"no challenge":                 {func(r *mcp.AuthorizeRequest) { r.Challenge = "" }, mcp.ErrChallengeInvalid},
		"plain method":                 {func(r *mcp.AuthorizeRequest) { r.ChallengeMethod = "plain" }, mcp.ErrChallengeInvalid},
	}
	for name, tc := range cases {
		request := valid
		tc.change(&request)
		if _, err := f.Authorize(ctx, request); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}
	code, err := f.Authorize(ctx, valid)
	if err != nil || !strings.HasPrefix(code, "occ_") {
		t.Fatalf("valid: %q %v", code, err)
	}
	// Une URI déclarée ouvre le redirect distant.
	if _, err := f.SetRedirectURIs(ctx, []string{"https://claude.ai/api/mcp/auth_callback"}); err != nil {
		t.Fatal(err)
	}
	remote := valid
	remote.RedirectURI = "https://claude.ai/api/mcp/auth_callback"
	if _, err := f.Authorize(ctx, remote); err != nil {
		t.Fatalf("declared redirect: %v", err)
	}
	if ok, _ := f.RedirectAllowed(ctx, "https://claude.ai/api/mcp/auth_callback?x=1"); ok {
		t.Fatal("a declared uri is matched exactly, never by prefix")
	}
}

func TestExchange_ThenVerify_ThenRefresh_ThenReplayRevokesTheFamily(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	secret := f.enable(t)
	code := f.authorize(t)
	first := f.exchange(t, secret, code)
	if !strings.HasPrefix(first.AccessToken, "oca_") || !strings.HasPrefix(first.RefreshToken, "ocr_") || first.ExpiresIn != 3600 {
		t.Fatalf("grant %+v", first)
	}
	// Le code ne sert qu'une fois.
	if _, err := f.Exchange(ctx, mcp.ExchangeRequest{ClientID: mcp.ClientID, ClientSecret: secret, Code: code, RedirectURI: callback, Verifier: verifier}); !errors.Is(err, mcp.ErrCodeInvalid) {
		t.Fatalf("code reused: %v", err)
	}
	token, err := f.Verify(ctx, first.AccessToken)
	if err != nil || token.Kind != mcp.KindAccess {
		t.Fatalf("verify access: %+v %v", token, err)
	}
	if _, err := f.Verify(ctx, first.RefreshToken); !errors.Is(err, mcp.ErrTokenInvalid) {
		t.Fatalf("a refresh token is not a bearer: %v", err)
	}
	sessions, err := f.Sessions(ctx)
	if err != nil || len(sessions) != 1 || sessions[0].FamilyID != token.FamilyID || !sessions[0].LastUsedAt.Equal(f.clock) {
		t.Fatalf("sessions %+v %v", sessions, err)
	}
	// L'accès expire au bout d'une heure ; le rafraîchissement le remplace.
	f.clock = f.clock.Add(mcp.AccessTTL)
	if _, err := f.Verify(ctx, first.AccessToken); !errors.Is(err, mcp.ErrTokenInvalid) {
		t.Fatalf("expired access: %v", err)
	}
	second, err := f.Refresh(ctx, mcp.RefreshRequest{ClientID: mcp.ClientID, ClientSecret: secret, RefreshToken: first.RefreshToken})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Verify(ctx, second.AccessToken); err != nil {
		t.Fatalf("second access: %v", err)
	}
	// Le premier rafraîchissement rejoué : toute la famille tombe.
	_, err = f.Refresh(ctx, mcp.RefreshRequest{ClientID: mcp.ClientID, ClientSecret: secret, RefreshToken: first.RefreshToken})
	if !errors.Is(err, mcp.ErrTokenReplayed) {
		t.Fatalf("replay: %v", err)
	}
	if _, err := f.Verify(ctx, second.AccessToken); !errors.Is(err, mcp.ErrTokenInvalid) {
		t.Fatalf("access of a revoked family: %v", err)
	}
	if _, err := f.Refresh(ctx, mcp.RefreshRequest{ClientID: mcp.ClientID, ClientSecret: secret, RefreshToken: second.RefreshToken}); !errors.Is(err, mcp.ErrTokenReplayed) {
		t.Fatalf("refresh of a revoked family: %v", err)
	}
	if sessions, _ := f.Sessions(ctx); len(sessions) != 0 {
		t.Fatalf("revoked family still listed: %+v", sessions)
	}
	if _, err := f.Refresh(ctx, mcp.RefreshRequest{ClientID: mcp.ClientID, ClientSecret: secret, RefreshToken: "ocr_unknown"}); !errors.Is(err, mcp.ErrTokenInvalid) {
		t.Fatalf("unknown refresh: %v", err)
	}
}

func TestExchange_RefusesBadSecretRedirectAndVerifier(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	secret := f.enable(t)
	valid := mcp.ExchangeRequest{ClientID: mcp.ClientID, ClientSecret: secret, Code: f.authorize(t), RedirectURI: callback, Verifier: verifier}
	cases := map[string]struct {
		change func(*mcp.ExchangeRequest)
		want   error
	}{
		"secret":   {func(r *mcp.ExchangeRequest) { r.ClientSecret = "ocs_wrong" }, mcp.ErrSecretInvalid},
		"client":   {func(r *mcp.ExchangeRequest) { r.ClientID = "other" }, mcp.ErrSecretInvalid},
		"code":     {func(r *mcp.ExchangeRequest) { r.Code = "occ_unknown" }, mcp.ErrCodeInvalid},
		"redirect": {func(r *mcp.ExchangeRequest) { r.RedirectURI = "http://127.0.0.1:1/other" }, mcp.ErrCodeInvalid},
		"verifier": {func(r *mcp.ExchangeRequest) { r.Verifier = "not-the-one" }, mcp.ErrVerifierInvalid},
	}
	for name, tc := range cases {
		request := valid
		request.Code = f.authorize(t)
		tc.change(&request)
		if _, err := f.Exchange(ctx, request); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}
	// Un code périmé ne s'échange plus.
	expired := valid
	expired.Code = f.authorize(t)
	f.clock = f.clock.Add(mcp.CodeTTL)
	if _, err := f.Exchange(ctx, expired); !errors.Is(err, mcp.ErrCodeInvalid) {
		t.Fatalf("expired code: %v", err)
	}
}

func TestExchange_SameCodeConcurrently_OnlyOneWins(t *testing.T) {
	f := newFixture(t)
	secret := f.enable(t)
	code := f.authorize(t)
	const attempts = 16
	var wg sync.WaitGroup
	start := make(chan struct{})
	outcomes := make([]error, attempts)
	for i := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, outcomes[i] = f.Exchange(context.Background(), mcp.ExchangeRequest{ClientID: mcp.ClientID, ClientSecret: secret, Code: code, RedirectURI: callback, Verifier: verifier})
		}()
	}
	close(start)
	wg.Wait()
	wins := 0
	for _, err := range outcomes {
		if err == nil {
			wins++
		} else if !errors.Is(err, mcp.ErrCodeInvalid) {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d winners", wins)
	}
}

func TestRefresh_SameTokenConcurrently_OnlyOneWinsAndTheFamilyFalls(t *testing.T) {
	f := newFixture(t)
	secret := f.enable(t)
	grant := f.exchange(t, secret, f.authorize(t))
	const attempts = 16
	var wg sync.WaitGroup
	start := make(chan struct{})
	outcomes := make([]error, attempts)
	for i := range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, outcomes[i] = f.Refresh(context.Background(), mcp.RefreshRequest{ClientID: mcp.ClientID, ClientSecret: secret, RefreshToken: grant.RefreshToken})
		}()
	}
	close(start)
	wg.Wait()
	wins := 0
	for _, err := range outcomes {
		if err == nil {
			wins++
		} else if !errors.Is(err, mcp.ErrTokenReplayed) {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d winners", wins)
	}
	// Le perdant a compté pour un rejeu : la famille est révoquée, y
	// compris la paire que le gagnant venait d'obtenir.
	if sessions, _ := f.Sessions(context.Background()); len(sessions) != 0 {
		t.Fatalf("family survived a replay: %+v", sessions)
	}
}

func TestRegenerateSecret_RevokesOAuthAndKeepsAPITokens(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	secret := f.enable(t)
	grant := f.exchange(t, secret, f.authorize(t))
	apiToken, _, err := f.CreateAPIToken(ctx, "script")
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := f.RegenerateSecret(ctx)
	if err != nil || fresh == secret {
		t.Fatalf("regenerate: %q %v", fresh, err)
	}
	if _, err := f.Verify(ctx, grant.AccessToken); !errors.Is(err, mcp.ErrTokenInvalid) {
		t.Fatalf("old access after regenerate: %v", err)
	}
	if _, err := f.Verify(ctx, apiToken); err != nil {
		t.Fatalf("api token after regenerate: %v", err)
	}
	if _, err := f.Exchange(ctx, mcp.ExchangeRequest{ClientID: mcp.ClientID, ClientSecret: secret, Code: f.authorize(t), RedirectURI: callback, Verifier: verifier}); !errors.Is(err, mcp.ErrSecretInvalid) {
		t.Fatalf("old secret: %v", err)
	}
	f.exchange(t, fresh, f.authorize(t))
}

func TestRevokeSession_CutsTheFamily(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	secret := f.enable(t)
	grant := f.exchange(t, secret, f.authorize(t))
	sessions, _ := f.Sessions(ctx)
	if err := f.RevokeSession(ctx, sessions[0].FamilyID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Verify(ctx, grant.AccessToken); !errors.Is(err, mcp.ErrTokenInvalid) {
		t.Fatalf("access after revoke: %v", err)
	}
	if err := f.RevokeSession(ctx, sessions[0].FamilyID); !errors.Is(err, mcp.ErrNotFound) {
		t.Fatalf("revoke twice: %v", err)
	}
}

func TestPurge_DropsExpiredAndKeepsRevokedUntilTheirEnd(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	secret := f.enable(t)
	code := f.authorize(t)
	grant := f.exchange(t, secret, f.authorize(t))
	second, err := f.Refresh(ctx, mcp.RefreshRequest{ClientID: mcp.ClientID, ClientSecret: secret, RefreshToken: grant.RefreshToken})
	if err != nil {
		t.Fatal(err)
	}
	// À mi-chemin : le code et le premier accès sont échus, le premier
	// rafraîchissement, révoqué, ne l'est pas encore : rejoué, il trahit.
	f.clock = f.clock.Add(2 * time.Hour)
	if err := f.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Exchange(ctx, mcp.ExchangeRequest{ClientID: mcp.ClientID, ClientSecret: secret, Code: code, RedirectURI: callback, Verifier: verifier}); !errors.Is(err, mcp.ErrCodeInvalid) {
		t.Fatalf("purged code: %v", err)
	}
	if _, err := f.Refresh(ctx, mcp.RefreshRequest{ClientID: mcp.ClientID, ClientSecret: secret, RefreshToken: grant.RefreshToken}); !errors.Is(err, mcp.ErrTokenReplayed) {
		t.Fatalf("replay after purge: %v", err)
	}
	// Trente jours plus tard, tout est parti : le rejeu devient inconnu.
	f.clock = f.clock.Add(mcp.RefreshTTL)
	if err := f.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	for _, refresh := range []string{grant.RefreshToken, second.RefreshToken} {
		if _, err := f.Refresh(ctx, mcp.RefreshRequest{ClientID: mcp.ClientID, ClientSecret: secret, RefreshToken: refresh}); !errors.Is(err, mcp.ErrTokenInvalid) {
			t.Fatalf("after full purge: %v", err)
		}
	}
}
