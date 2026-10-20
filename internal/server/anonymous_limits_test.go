package server

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/auth"
)

func TestAuthBasic_AttemptBudget_IsSharedByAnIPv6SixtyFour(t *testing.T) {
	server := newTestServer(t)
	server.enableBasic(t)
	attempt := func(peer, password string) int {
		request := httptest.NewRequest(http.MethodGet, "/api/counts", nil)
		request.RemoteAddr = peer
		request.SetBasicAuth(testUsername, password)
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		return recorder.Code
	}
	for i := range authAttemptBurst {
		if got := attempt("[2001:db8:1:2::1]:4000", "guess"); got != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status %d", i+1, got)
		}
	}
	if got := attempt("[2001:db8:1:2::2]:4000", testPassword); got != http.StatusTooManyRequests {
		t.Fatalf("same /64: status %d, want 429", got)
	}
	if got := attempt("[2001:db8:1:3::1]:4000", testPassword); got != http.StatusOK {
		t.Fatalf("another /64: status %d, want 200", got)
	}
}

// Les autres limiteurs par adresse passent par la même clé ; ceux de
// l'agent et des pings se testent dans leurs paquets.
func TestLimiters_ShareOneBucketPerIPv6SixtyFour(t *testing.T) {
	server := newTestServer(t)
	first, second := "2001:db8:9:9::1", "2001:db8:9:9::2"
	cases := []struct {
		name  string
		burst int
		allow func(string) bool
	}{
		{"public read", statusPerSourceBurst, server.statusLimits.allowRead},
		{"public stream opening", statusStreamBurst, server.statusLimits.allowStream},
		{"mcp", mcpPerSourceBurst, server.mcpLimits.allow},
		{"mcp token", mcpTokenBurst, server.mcpLimits.allowToken},
	}
	for _, c := range cases {
		for i := range c.burst {
			if !c.allow(first) {
				t.Fatalf("%s: request %d of the burst refused", c.name, i+1)
			}
		}
		if c.allow(second) {
			t.Errorf("%s: a second address of the same /64 got its own bucket", c.name)
		}
	}
}

// Le verdict « file pleine » est un 429 avec Retry-After, comme le limiteur ;
// une requête abandonnée n'est pas un mauvais mot de passe.
type refusingAuth struct {
	AuthService
	err error
}

func (a refusingAuth) Verify(context.Context, auth.State, string, string) (bool, error) {
	return false, a.err
}

func TestAuthBasic_FullVerificationQueue_Is429WithRetryAfter(t *testing.T) {
	server := newTestServer(t)
	server.enableBasic(t)
	server.auth = refusingAuth{AuthService: server.operator, err: auth.ErrVerificationQueueFull}
	recorder := callAs(server.Server, http.MethodGet, "/api/counts", testUsername, "guess")
	if recorder.Code != http.StatusTooManyRequests || recorder.Header().Get("Retry-After") == "" {
		t.Fatalf("status %d, Retry-After %q", recorder.Code, recorder.Header().Get("Retry-After"))
	}
	if recorder.Header().Get("WWW-Authenticate") != "" {
		t.Fatal("a full queue is not a refusal of the credentials: no Basic challenge")
	}
}

func TestAuthBasic_CanceledWait_IsNotAFailedLogin(t *testing.T) {
	server := newTestServer(t)
	server.enableBasic(t)
	server.auth = refusingAuth{AuthService: server.operator, err: context.Canceled}
	recorder := callAs(server.Server, http.MethodGet, "/api/counts", testUsername, "guess")
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", recorder.Code)
	}
}

// Le test de bout en bout : l'opérateur dont les identifiants sont retenus
// passe même quand la file des anonymes est pleine.
func TestAuthBasic_RememberedOperator_PassesWhileTheQueueIsRefusing(t *testing.T) {
	server := newTestServer(t)
	server.enableBasic(t)
	if got := callAs(server.Server, http.MethodGet, "/api/counts", testUsername, testPassword); got.Code != http.StatusOK {
		t.Fatalf("first login: status %d", got.Code)
	}
	server.auth = refusingAuth{AuthService: server.operator, err: auth.ErrVerificationQueueFull}
	if got := callAs(server.Server, http.MethodGet, "/api/counts", testUsername, testPassword); got.Code != http.StatusOK {
		t.Fatalf("remembered operator: status %d", got.Code)
	}
}

// publicStreamFrom ouvre le direct public depuis l'adresse de pair donnée :
// l'enveloppe pose RemoteAddr d'après un en-tête de test, le reste est le
// vrai chemin (limiteur, places par source, pool global).
func publicStreamFrom(t *testing.T, server *testServer, peer string) (*http.Response, context.CancelFunc) {
	t.Helper()
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = r.Header.Get("X-Test-Peer")
		server.ServeHTTP(w, r)
	})
	liveServer := httptest.NewServer(wrapped)
	t.Cleanup(liveServer.Close)
	ctx, cancel := context.WithCancel(context.Background())
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, liveServer.URL+"/statut/api/events", nil)
	request.Header.Set("X-Test-Peer", peer)
	response, err := liveServer.Client().Do(request)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); response.Body.Close() })
	return response, cancel
}

func TestPublicEvents_OneSourceCannotHoldMoreThanItsShare(t *testing.T) {
	server := newTestServer(t)
	holder := "198.51.100.7:5000"
	var cancels []context.CancelFunc
	for i := range maxPublicStreamsPerSource {
		response, cancel := publicStreamFrom(t, server, holder)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("stream %d: status %d", i+1, response.StatusCode)
		}
		cancels = append(cancels, cancel)
	}
	response, _ := publicStreamFrom(t, server, holder)
	if response.StatusCode != http.StatusTooManyRequests || response.Header.Get("Retry-After") == "" {
		t.Fatalf("beyond the share: status %d, Retry-After %q", response.StatusCode, response.Header.Get("Retry-After"))
	}
	if taken := server.publicTabs.taken.Load(); taken != maxPublicStreamsPerSource {
		t.Fatalf("the refused stream took a place of the global pool: %d taken", taken)
	}

	// Une autre source passe.
	if other, _ := publicStreamFrom(t, server, "203.0.113.50:5000"); other.StatusCode != http.StatusOK {
		t.Fatalf("another source: status %d", other.StatusCode)
	}

	// Une place rendue se reprend (le seau d'ouverture est rempli entre-temps).
	cancels[0]()
	waitFor(t, func() bool { return server.publicTabs.taken.Load() == maxPublicStreamsPerSource })
	server.statusLimits.setClock(func() time.Time { return testNow.Add(time.Minute) })
	if again, _ := publicStreamFrom(t, server, holder); again.StatusCode != http.StatusOK {
		t.Fatalf("after a stream ended: status %d", again.StatusCode)
	}
}

func TestPublicEvents_ShareIsPerIPv6SixtyFour(t *testing.T) {
	server := newTestServer(t)
	for i := range maxPublicStreamsPerSource {
		peer := "[2001:db8:5:5::" + string(rune('1'+i)) + "]:5000"
		if response, _ := publicStreamFrom(t, server, peer); response.StatusCode != http.StatusOK {
			t.Fatalf("stream %d: status %d", i+1, response.StatusCode)
		}
	}
	if response, _ := publicStreamFrom(t, server, "[2001:db8:5:5:ffff::9]:5000"); response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("same /64: status %d, want 429", response.StatusCode)
	}
	if response, _ := publicStreamFrom(t, server, "[2001:db8:5:6::1]:5000"); response.StatusCode != http.StatusOK {
		t.Fatalf("another /64: status %d", response.StatusCode)
	}
}

// Un flux public finit de lui-même à l'échéance, proprement : la place
// retourne au pool et le navigateur se reconnecte.
func TestPublicEvents_StreamEndsAfterItsMaximumLifetime(t *testing.T) {
	server := newTestServer(t)
	server.publicStreamLifetime = 300 * time.Millisecond
	response, _ := publicStreamFrom(t, server, "198.51.100.7:5000")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status %d", response.StatusCode)
	}
	finished := make(chan error, 1)
	go func() {
		_, err := bufio.NewReader(response.Body).WriteTo(discard{})
		finished <- err
	}()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("the stream must end cleanly: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stream outlived its maximum lifetime")
	}
	waitFor(t, func() bool { return server.publicTabs.taken.Load() == 0 })
	if held := len(server.publicStreamsBySource.taken); held != 0 {
		t.Fatalf("%d sources still hold a share", held)
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func TestSourceSlots_TakeUpToTheLimitThenGiveBack(t *testing.T) {
	places := newSourceSlots(2)
	for range 2 {
		if !places.take("a") {
			t.Fatal("a source must get its share")
		}
	}
	if places.take("a") {
		t.Fatal("a source must not get more than its share")
	}
	if !places.take("b") {
		t.Fatal("another source has its own share")
	}
	places.release("a")
	if !places.take("a") {
		t.Fatal("a released place must be taken again")
	}
	places.release("a")
	places.release("a")
	places.release("b")
	if len(places.taken) != 0 {
		t.Fatalf("released sources must leave no entry: %v", places.taken)
	}
}
