package apitoken_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/apitoken"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
)

var testNow = time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)

// clock est une horloge que le test avance.
type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

func newService(t *testing.T) (*apitoken.Service, *clock, *store.DB, *bytes.Buffer) {
	t.Helper()
	db := storetest.Open(t)
	var logs bytes.Buffer
	service := apitoken.New(db, slog.New(slog.NewTextHandler(&logs, nil)))
	tick := &clock{now: testNow}
	service.SetClock(tick.Now)
	return service, tick, db, &logs
}

func ptr(days int) *int { return &days }

func TestCreate_DefaultsToReadAndNinetyDays(t *testing.T) {
	service, _, _, logs := newService(t)
	ctx := context.Background()
	cleartext, token, err := service.Create(ctx, apitoken.Request{Name: "  mcp-maison  "})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cleartext, brand.APITokenScheme) || len(cleartext) < 40 {
		t.Fatalf("cleartext %q", cleartext)
	}
	if token.Scope != apitoken.ScopeRead || token.Name != "mcp-maison" || !token.ExpiresAt.Equal(testNow.Add(90*24*time.Hour)) {
		t.Fatalf("token %+v", token)
	}
	if token.Hash == cleartext || strings.Contains(token.Hash, cleartext) || !strings.HasPrefix(cleartext, strings.TrimSuffix(token.Masked(), "…")) {
		t.Fatalf("hash %q, masked %q", token.Hash, token.Masked())
	}
	if strings.Contains(logs.String(), cleartext) {
		t.Fatal("the process log carries the token")
	}
	verified, err := service.Verify(ctx, cleartext)
	if err != nil || verified.ID != token.ID {
		t.Fatalf("verify: %+v %v", verified, err)
	}
	// Deux tirages ne donnent jamais le même jeton.
	other, _, err := service.Create(ctx, apitoken.Request{Name: "autre"})
	if err != nil || other == cleartext {
		t.Fatalf("second token %q %v", other, err)
	}
}

func TestCreate_RefusesWhatTheListDoesNotOffer(t *testing.T) {
	service, _, _, _ := newService(t)
	ctx := context.Background()
	cases := map[string]struct {
		request apitoken.Request
		want    error
	}{
		"empty name":        {apitoken.Request{Name: "   "}, apitoken.ErrNameInvalid},
		"long name":         {apitoken.Request{Name: strings.Repeat("é", apitoken.MaxNameLength+1)}, apitoken.ErrNameInvalid},
		"verbs on read":     {apitoken.Request{Name: "x", Verbs: []string{"probe.pause"}}, apitoken.ErrVerbsNeedWrite},
		"unknown verb":      {apitoken.Request{Name: "x", Scope: apitoken.ScopeWrite, Verbs: []string{"machine.delete"}}, apitoken.ErrVerbUnknown},
		"unknown scope":     {apitoken.Request{Name: "x", Scope: "admin"}, apitoken.ErrScopeInvalid},
		"unlisted lifetime": {apitoken.Request{Name: "x", Days: ptr(7)}, apitoken.ErrLifetimeInvalid},
		"negative":          {apitoken.Request{Name: "x", Days: ptr(-1)}, apitoken.ErrLifetimeInvalid},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := service.Create(ctx, c.request); !errors.Is(err, c.want) {
				t.Fatalf("err %v, want %v", err, c.want)
			}
		})
	}
	if tokens, _ := service.List(ctx); len(tokens) != 0 {
		t.Fatalf("a refused request left %d token(s)", len(tokens))
	}
}

func TestCreate_EveryOfferedLifetimeWorks(t *testing.T) {
	service, _, _, _ := newService(t)
	ctx := context.Background()
	for _, days := range apitoken.Lifetimes() {
		_, token, err := service.Create(ctx, apitoken.Request{Name: fmt.Sprintf("durée %d", days), Days: ptr(days)})
		if err != nil {
			t.Fatalf("%d days: %v", days, err)
		}
		if days == 0 && !token.ExpiresAt.IsZero() {
			t.Fatalf("no expiry asked, got %v", token.ExpiresAt)
		}
		if days != 0 && !token.ExpiresAt.Equal(testNow.Add(time.Duration(days)*24*time.Hour)) {
			t.Fatalf("%d days: expires %v", days, token.ExpiresAt)
		}
	}
}

func TestCreate_StopsAtTheCeilingAndARevocationFreesASlot(t *testing.T) {
	service, _, _, _ := newService(t)
	ctx := context.Background()
	var first apitoken.Token
	for i := range apitoken.MaxTokens {
		_, token, err := service.Create(ctx, apitoken.Request{Name: fmt.Sprintf("jeton %d", i)})
		if err != nil {
			t.Fatalf("token %d: %v", i, err)
		}
		if i == 0 {
			first = token
		}
	}
	if _, _, err := service.Create(ctx, apitoken.Request{Name: "de trop"}); !errors.Is(err, apitoken.ErrTooMany) {
		t.Fatalf("over the ceiling: %v", err)
	}
	if err := service.Revoke(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Create(ctx, apitoken.Request{Name: "à nouveau"}); err != nil {
		t.Fatalf("after a revocation: %v", err)
	}
}

// Vingt créations simultanées ne passent pas toutes sur les dernières places.
func TestCreate_TheCeilingHoldsUnderConcurrency(t *testing.T) {
	service, _, _, _ := newService(t)
	ctx := context.Background()
	const attempts = 24
	results := make(chan error, attempts)
	var wait sync.WaitGroup
	for attempt := range attempts {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, _, err := service.Create(ctx, apitoken.Request{Name: fmt.Sprintf("course %d", attempt)})
			results <- err
		}()
	}
	wait.Wait()
	close(results)
	created, refused := 0, 0
	for err := range results {
		switch {
		case err == nil:
			created++
		case errors.Is(err, apitoken.ErrTooMany):
			refused++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if created != apitoken.MaxTokens || refused != attempts-apitoken.MaxTokens {
		t.Fatalf("created %d, refused %d", created, refused)
	}
	if tokens, _ := service.List(ctx); len(tokens) != apitoken.MaxTokens {
		t.Fatalf("%d tokens stored", len(tokens))
	}
}

func TestVerify_RefusesUnknownRevokedAndExpiredTheSameWay(t *testing.T) {
	service, tick, _, _ := newService(t)
	ctx := context.Background()
	if _, err := service.Verify(ctx, brand.APITokenScheme+"inconnu"); !errors.Is(err, apitoken.ErrInvalid) {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := service.Verify(ctx, ""); !errors.Is(err, apitoken.ErrInvalid) {
		t.Fatalf("empty: %v", err)
	}
	revoked, token, _ := service.Create(ctx, apitoken.Request{Name: "révoqué"})
	if err := service.Revoke(ctx, token.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Verify(ctx, revoked); !errors.Is(err, apitoken.ErrInvalid) {
		t.Fatalf("revoked: %v", err)
	}
	if err := service.Revoke(ctx, token.ID); !errors.Is(err, apitoken.ErrNotFound) {
		t.Fatalf("revoke twice: %v", err)
	}
	short, _, _ := service.Create(ctx, apitoken.Request{Name: "court", Days: ptr(30)})
	forever, _, _ := service.Create(ctx, apitoken.Request{Name: "sans fin", Days: ptr(0)})
	tick.now = testNow.Add(30 * 24 * time.Hour)
	if _, err := service.Verify(ctx, short); !errors.Is(err, apitoken.ErrInvalid) {
		t.Fatalf("expired at its deadline: %v", err)
	}
	if _, err := service.Verify(ctx, forever); err != nil {
		t.Fatalf("no expiry: %v", err)
	}
	if tokens, _ := service.List(ctx); len(tokens) != 1 {
		t.Fatalf("an expired token is still listed: %+v", tokens)
	}
}

// Le dernier usage se note au plus une fois par minute : un client qui
// enchaîne les lectures n'écrit pas à chacune.
func TestVerify_NotesTheLastUseAtMostOncePerMinute(t *testing.T) {
	service, tick, db, _ := newService(t)
	ctx := context.Background()
	cleartext, token, _ := service.Create(ctx, apitoken.Request{Name: "lecteur"})
	lastUsed := func() time.Time {
		t.Helper()
		stored, err := db.GetAPIToken(ctx, apitoken.HashSecret(cleartext))
		if err != nil {
			t.Fatal(err)
		}
		return stored.LastUsedAt
	}
	if !lastUsed().IsZero() {
		t.Fatal("never used yet")
	}
	if _, err := service.Verify(ctx, cleartext); err != nil {
		t.Fatal(err)
	}
	first := lastUsed()
	if !first.Equal(testNow.Truncate(time.Second)) {
		t.Fatalf("first use %v", first)
	}
	tick.now = testNow.Add(30 * time.Second)
	if _, err := service.Verify(ctx, cleartext); err != nil {
		t.Fatal(err)
	}
	if got := lastUsed(); !got.Equal(first) {
		t.Fatalf("written again within the minute: %v", got)
	}
	tick.now = testNow.Add(61 * time.Second)
	verified, err := service.Verify(ctx, cleartext)
	if err != nil || !verified.LastUsedAt.Equal(tick.now) || verified.ID != token.ID {
		t.Fatalf("after the minute: %+v %v", verified, err)
	}
	if got := lastUsed(); !got.Equal(tick.now.Truncate(time.Second)) {
		t.Fatalf("not written after the minute: %v", got)
	}
}

func TestRevokeAll_CountsTheLiveTokensAndLeavesNothing(t *testing.T) {
	service, tick, _, _ := newService(t)
	ctx := context.Background()
	live, _, _ := service.Create(ctx, apitoken.Request{Name: "vivant"})
	forever, _, _ := service.Create(ctx, apitoken.Request{Name: "éternel", Days: ptr(0)})
	if _, _, err := service.Create(ctx, apitoken.Request{Name: "court", Days: ptr(30)}); err != nil {
		t.Fatal(err)
	}
	tick.now = testNow.Add(31 * 24 * time.Hour)
	// Le jeton de 90 jours vit encore, celui de 30 est échu.
	revoked, err := service.RevokeAll(ctx)
	if err != nil || revoked != 2 {
		t.Fatalf("revoked %d, %v", revoked, err)
	}
	for _, cleartext := range []string{live, forever} {
		if _, err := service.Verify(ctx, cleartext); !errors.Is(err, apitoken.ErrInvalid) {
			t.Fatalf("a token survived: %v", err)
		}
	}
	if again, err := service.RevokeAll(ctx); err != nil || again != 0 {
		t.Fatalf("second pass %d, %v", again, err)
	}
}

func TestPurge_RemovesTheExpiredOnly(t *testing.T) {
	service, tick, db, _ := newService(t)
	ctx := context.Background()
	if _, _, err := service.Create(ctx, apitoken.Request{Name: "court", Days: ptr(30)}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Create(ctx, apitoken.Request{Name: "sans fin", Days: ptr(0)}); err != nil {
		t.Fatal(err)
	}
	tick.now = testNow.Add(40 * 24 * time.Hour)
	if err := service.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := db.ListAPITokens(ctx)
	if err != nil || len(rows) != 1 || rows[0].Name != "sans fin" {
		t.Fatalf("rows %+v %v", rows, err)
	}
}

func TestWatch_StopsWithItsContext(t *testing.T) {
	service, _, _, _ := newService(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		service.Watch(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Watch did not stop")
	}
}

// Le journal d'audit dit le nom, la portée et l'échéance d'un jeton créé,
// jamais le jeton ni son préfixe ; révoquer tout dit combien.
func TestAudit_NamesTheTokenNeverItsSecret(t *testing.T) {
	service, _, db, _ := newService(t)
	recorder := audit.New(db, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	recorder.SetClock(func() time.Time { return testNow })
	service.SetAuditor(recorder)
	ctx := audit.WithActor(context.Background(), audit.Operator("lucas"))

	cleartext, token, err := service.Create(ctx, apitoken.Request{Name: "mcp-maison", Days: ptr(0)})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Revoke(ctx, token.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Create(ctx, apitoken.Request{Name: "second"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RevokeAll(ctx); err != nil {
		t.Fatal(err)
	}
	events, err := db.ListAuditEvents(context.Background(), audit.Query{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 {
		t.Fatalf("events: %+v", events)
	}
	// Du plus récent au plus ancien.
	all, second, revoke, first := events[0], events[1], events[2], events[3]
	if first.Action != audit.APITokenCreate || first.ObjectID != token.ID || first.ObjectName != "mcp-maison" ||
		first.Details["scope"] != "read" || first.Details["expires_at"] != "never" || first.ObjectKind != audit.ObjectAPIToken {
		t.Fatalf("create event %+v", first)
	}
	if second.Details["expires_at"] == "never" || second.Details["expires_at"] == "" {
		t.Fatalf("second create %+v", second)
	}
	if revoke.Action != audit.APITokenRevoke || revoke.ObjectID != token.ID {
		t.Fatalf("revoke event %+v", revoke)
	}
	if all.Action != audit.APITokenRevoke || all.Details["all"] != "true" || all.Details["api_tokens"] != "1" {
		t.Fatalf("revoke all event %+v", all)
	}
	for _, event := range events {
		for _, value := range event.Details {
			if strings.Contains(value, cleartext) || strings.Contains(value, token.Prefix) {
				t.Fatalf("event %+v carries the secret", event)
			}
		}
	}
}

func TestCreate_WriteTokenCarriesItsVerbs(t *testing.T) {
	service, _, _, _ := newService(t)
	ctx := context.Background()
	cleartext, token, err := service.Create(ctx, apitoken.Request{
		Name: "agent", Scope: apitoken.ScopeWrite, Verbs: []string{"probe.pause", "agent.ping", "probe.pause"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"agent.ping", "probe.pause"}
	if !slices.Equal(token.Verbs, want) {
		t.Fatalf("created verbs %v, want %v", token.Verbs, want)
	}
	verified, err := service.Verify(ctx, cleartext)
	if err != nil || !slices.Equal(verified.Verbs, want) {
		t.Fatalf("verified %+v %v", verified, err)
	}
	if !verified.CanDo("probe.pause") || verified.CanDo("probe.resume") {
		t.Fatal("CanDo does not follow the verbs")
	}
	listed, _ := service.List(ctx)
	if len(listed) != 1 || !slices.Equal(listed[0].Verbs, want) {
		t.Fatalf("listed %+v", listed)
	}
}

func TestCreate_WriteTokenWithoutVerbCanDoNothing(t *testing.T) {
	service, _, _, _ := newService(t)
	_, token, err := service.Create(context.Background(), apitoken.Request{Name: "muet", Scope: apitoken.ScopeWrite})
	if err != nil || len(token.Verbs) != 0 || token.CanDo("agent.ping") {
		t.Fatalf("%+v %v", token, err)
	}
}

func TestCreate_ReadTokenNeverCanDo(t *testing.T) {
	token := apitoken.Token{Scope: apitoken.ScopeRead, Verbs: []string{"agent.ping"}}
	if token.CanDo("agent.ping") {
		t.Fatal("a read token carries no verb")
	}
}

// Deux jetons vivants au même nom ne se distinguent plus à l'audit : le
// second est refusé, sans égard à la casse ; un nom libéré par une
// révocation redevient disponible.
func TestCreate_RefusesANameALiveTokenAlreadyBears(t *testing.T) {
	service, _, _, _ := newService(t)
	ctx := context.Background()
	_, first, err := service.Create(ctx, apitoken.Request{Name: "agent-veille"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"agent-veille", "  agent-veille ", "Agent-Veille"} {
		if _, _, err := service.Create(ctx, apitoken.Request{Name: name}); !errors.Is(err, apitoken.ErrNameTaken) {
			t.Fatalf("%q: %v", name, err)
		}
	}
	if key, _ := refusal.KeyOf(apitoken.ErrNameTaken); key != "api_token.name_taken" || !refusal.IsConflict(apitoken.ErrNameTaken) {
		t.Fatalf("refusal %q", key)
	}
	if err := service.Revoke(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Create(ctx, apitoken.Request{Name: "agent-veille"}); err != nil {
		t.Fatalf("after a revocation: %v", err)
	}
}
