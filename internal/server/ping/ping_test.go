package ping

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/heartbeat"
)

func TestPing_ThreeFormsInGetAndPost_MoveTheJob(t *testing.T) {
	server := newBench(t)
	job := server.createJob(t, "job")
	cases := []struct {
		method, path string
		status       heartbeat.Status
	}{
		{http.MethodGet, "/ping/" + job.Token + "/start", heartbeat.StatusStarted},
		{http.MethodPost, "/ping/" + job.Token, heartbeat.StatusOnTime},
		{http.MethodPost, "/ping/" + job.Token + "/start", heartbeat.StatusStarted},
		{http.MethodGet, "/ping/" + job.Token + "/3", heartbeat.StatusFailed},
		{http.MethodGet, "/ping/" + job.Token + "/0", heartbeat.StatusOnTime},
	}
	for _, tc := range cases {
		recorder := server.send(tc.method, tc.path, "", "")
		if recorder.Code != http.StatusOK || recorder.Body.String() != "ok\n" {
			t.Fatalf("%s %s: %d %q", tc.method, tc.path, recorder.Code, recorder.Body.String())
		}
		if len(recorder.Result().Cookies()) != 0 {
			t.Fatalf("%s: a ping must not set a cookie", tc.path)
		}
		got, _ := server.heartbeats.Get(context.Background(), job.ID)
		if got.Status != tc.status {
			t.Fatalf("%s %s: status %s, want %s", tc.method, tc.path, got.Status, tc.status)
		}
	}
	pings, _ := server.heartbeats.Pings(context.Background(), job.ID, 10)
	if len(pings) != 5 || pings[0].Method != "GET" || pings[0].Source != "192.0.2.1" {
		t.Fatalf("pings %+v", pings)
	}
}

func TestPing_RefusesUnknownTokenAndBadExitCode(t *testing.T) {
	server := newBench(t)
	job := server.createJob(t, "job")
	if got := server.send(http.MethodGet, "/ping/hb_nope", "", ""); got.Code != http.StatusNotFound {
		t.Errorf("unknown token: %d", got.Code)
	}
	for _, code := range []string{"256", "-1", "abc"} {
		if got := server.send(http.MethodGet, "/ping/"+job.Token+"/"+code, "", ""); got.Code != http.StatusBadRequest {
			t.Errorf("code %s: %d", code, got.Code)
		}
	}
}

func TestPing_PostBody_IsKeptAndTruncated(t *testing.T) {
	server := newBench(t)
	job := server.createJob(t, "job")
	body := strings.Repeat("x", heartbeat.MaxPayloadBytes+500)
	if got := server.send(http.MethodPost, "/ping/"+job.Token+"/0", body, ""); got.Code != http.StatusOK {
		t.Fatalf("status %d", got.Code)
	}
	runs, _ := server.heartbeats.Runs(context.Background(), job.ID, 1)
	if len(runs[0].Payload) != heartbeat.MaxPayloadBytes {
		t.Fatalf("payload kept %d bytes", len(runs[0].Payload))
	}
}

func TestPing_IsRateLimitedPerSourceAndPerToken(t *testing.T) {
	server := newBench(t)
	job := server.createJob(t, "job")
	other := server.createJob(t, "other")
	// La rafale par IP s'épuise d'abord, sur des jetons inconnus : les 404
	// comptent.
	for i := range pingPerSourceBurst {
		if got := server.send(http.MethodGet, "/ping/hb_unknown", "", ""); got.Code != http.StatusNotFound {
			t.Fatalf("request %d: %d", i+1, got.Code)
		}
	}
	got := server.send(http.MethodGet, "/ping/"+job.Token, "", "")
	if got.Code != http.StatusTooManyRequests || got.Header().Get("Retry-After") == "" {
		t.Fatalf("over the source burst: %d %v", got.Code, got.Header())
	}
	// Des adresses toutes différentes épuisent la rafale d'un seul jeton ;
	// l'autre jeton passe encore.
	for i := range pingPerTokenBurst {
		if got := server.send(http.MethodGet, "/ping/"+job.Token, "", source(i)); got.Code != http.StatusOK {
			t.Fatalf("token request %d: %d", i+1, got.Code)
		}
	}
	if got := server.send(http.MethodGet, "/ping/"+job.Token, "", source(99)); got.Code != http.StatusTooManyRequests {
		t.Fatalf("over the token burst: %d", got.Code)
	}
	if got := server.send(http.MethodGet, "/ping/"+other.Token, "", source(99)); got.Code != http.StatusOK {
		t.Fatalf("other token: %d", got.Code)
	}
}

// L'URL de ping qu'une page réaffiche vient de la base, où le jeton est
// chiffré : elle doit ouvrir le même moniteur que celle donnée à la création.
func TestPing_ByTheTokenTheDatabaseGivesBack(t *testing.T) {
	server := newBench(t)
	job := server.createJob(t, "job")
	stored, err := server.heartbeats.Get(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Token != job.Token {
		t.Fatalf("the stored token %q is not the one given at creation %q", stored.Token, job.Token)
	}
	recorder := server.send(http.MethodGet, "/ping/"+stored.Token, "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("ping by the re-displayed token: %d", recorder.Code)
	}
}

// Un cron oublié continue d'appeler l'URL d'une tâche en pause : la réponse
// reste « ok », le ping est noté, et rien d'autre ne bouge.
func TestPing_WhilePaused_Answers200AndChangesNothing(t *testing.T) {
	server := newBench(t)
	ctx := context.Background()
	job := server.createJob(t, "job")
	if got := server.send(http.MethodGet, "/ping/"+job.Token, "", ""); got.Code != http.StatusOK {
		t.Fatalf("ping: %d", got.Code)
	}
	if err := server.heartbeats.Pause(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	paused, _ := server.heartbeats.Get(ctx, job.ID)

	for _, path := range []string{"/ping/" + job.Token, "/ping/" + job.Token + "/start", "/ping/" + job.Token + "/3"} {
		recorder := server.send(http.MethodGet, path, "", "")
		if recorder.Code != http.StatusOK || recorder.Body.String() != "ok\n" {
			t.Fatalf("%s: %d %q", path, recorder.Code, recorder.Body.String())
		}
	}
	after, _ := server.heartbeats.Get(ctx, job.ID)
	if after.Status != heartbeat.StatusPaused || !after.LastPingAt.Equal(paused.LastPingAt) || !after.NextDeadlineAt.IsZero() || !after.RunStartedAt.IsZero() {
		t.Fatalf("a paused job moved: %+v", after)
	}
	if pings, _ := server.heartbeats.Pings(ctx, job.ID, 10); len(pings) != 4 {
		t.Fatalf("the pings must be kept: %d", len(pings))
	}
}

// Un jeton inventé d'un mégaoctet ne laisse rien en mémoire : 404 après la
// limite par source, sans seau par jeton.
func TestPing_OversizedToken_IsRefusedWithoutPerTokenState(t *testing.T) {
	server := newBench(t)
	path := "/ping/hb_" + strings.Repeat("a", 1<<20)
	for i := range pingPerSourceBurst {
		if got := server.send(http.MethodGet, path, "", ""); got.Code != http.StatusNotFound {
			t.Fatalf("ping %d: status %d, want 404", i+1, got.Code)
		}
	}
	if size := server.handler.limits.byToken.Size(); size != 0 {
		t.Fatalf("byToken holds %d buckets after oversized tokens", size)
	}
	// Le refus coûte quand même un jeton de la source : le flot s'arrête.
	if got := server.send(http.MethodGet, path, "", ""); got.Code != http.StatusTooManyRequests {
		t.Fatalf("after the source burst: status %d, want 429", got.Code)
	}
}

func TestPing_TokenAtTheLimit_IsLookedUpLikeAnyOther(t *testing.T) {
	server := newBench(t)
	path := "/ping/" + strings.Repeat("a", maxPingTokenLength)
	if got := server.send(http.MethodGet, path, "", ""); got.Code != http.StatusNotFound {
		t.Fatalf("status %d", got.Code)
	}
	if size := server.handler.limits.byToken.Size(); size != 1 {
		t.Fatalf("byToken holds %d buckets, want 1", size)
	}
}

// Un ping au vrai format compte toujours sur son seau, et la clé du seau
// ne porte pas le jeton.
func TestPing_RealToken_CountsInByToken(t *testing.T) {
	server := newBench(t)
	job := server.createJob(t, "job")
	for range 3 {
		if got := server.send(http.MethodGet, "/ping/"+job.Token, "", ""); got.Code != http.StatusOK {
			t.Fatalf("status %d", got.Code)
		}
	}
	if size := server.handler.limits.byToken.Size(); size != 1 {
		t.Fatalf("byToken holds %d buckets, want 1", size)
	}
	for range pingPerTokenBurst {
		server.handler.limits.byToken.Allow(job.Token)
	}
	// Le seau du jeton en clair n'est pas celui du ping : la clé est l'empreinte.
	if got := server.send(http.MethodGet, "/ping/"+job.Token, "", ""); got.Code != http.StatusOK {
		t.Fatalf("the plaintext token must not be a bucket key: status %d", got.Code)
	}
	if size := server.handler.limits.byToken.Size(); size != 2 {
		t.Fatalf("byToken holds %d buckets, want 2", size)
	}
}

// Deux adresses d'un même /64 sont une seule source pour le limiteur ; un
// autre /64 et une IPv4 ne sont pas touchés.
func TestPing_SourceBudget_IsSharedByAnIPv6SixtyFour(t *testing.T) {
	server := newBench(t)
	for i := range pingPerSourceBurst {
		if got := server.sendFrom("[2001:db8:1:2::1]:4000", "/ping/hb_nope"); got != http.StatusNotFound {
			t.Fatalf("ping %d: status %d", i+1, got)
		}
	}
	if got := server.sendFrom("[2001:db8:1:2:aaaa:bbbb:cccc:dddd]:4000", "/ping/hb_nope"); got != http.StatusTooManyRequests {
		t.Fatalf("same /64: status %d, want 429", got)
	}
	if got := server.sendFrom("[2001:db8:1:3::1]:4000", "/ping/hb_nope"); got != http.StatusNotFound {
		t.Fatalf("another /64: status %d, want 404", got)
	}
	if got := server.sendFrom("203.0.113.9:4000", "/ping/hb_nope"); got != http.StatusNotFound {
		t.Fatalf("an IPv4 address: status %d, want 404", got)
	}
}

// Le journal et la source du ping gardent l'adresse complète : seule la clé
// de limitation est agrégée.
func TestPing_RecordedSource_StaysTheFullAddress(t *testing.T) {
	server := newBench(t)
	job := server.createJob(t, "job")
	request := httptest.NewRequest(http.MethodGet, "/ping/"+job.Token, nil)
	request.RemoteAddr = "[2001:db8:1:2::7]:4000"
	server.ServeHTTP(httptest.NewRecorder(), request)
	pings, _ := server.heartbeats.Pings(context.Background(), job.ID, 10)
	if len(pings) != 1 || pings[0].Source != "2001:db8:1:2::7" {
		t.Fatalf("pings %+v", pings)
	}
}
