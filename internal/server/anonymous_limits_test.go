package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/auth"
)

func TestAuthBasic_AttemptBudget_IsSharedByAnIPv6SixtyFour(t *testing.T) {
	server := newFirstSetupServer(t)
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
	server := newFirstSetupServer(t)
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
	server := newFirstSetupServer(t)
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
	server := newFirstSetupServer(t)
	server.enableBasic(t)
	if got := callAs(server.Server, http.MethodGet, "/api/counts", testUsername, testPassword); got.Code != http.StatusOK {
		t.Fatalf("first login: status %d", got.Code)
	}
	server.auth = refusingAuth{AuthService: server.operator, err: auth.ErrVerificationQueueFull}
	if got := callAs(server.Server, http.MethodGet, "/api/counts", testUsername, testPassword); got.Code != http.StatusOK {
		t.Fatalf("remembered operator: status %d", got.Code)
	}
}
