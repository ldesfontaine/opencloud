package agentapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func (b *bench) bareSignal(session string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/agent/signal", nil)
	request.Header.Set("Authorization", "Bearer "+session)
	recorder := httptest.NewRecorder()
	b.ServeHTTP(recorder, request)
	return recorder
}

// Une machine qui signale en rafale est freinée : 429 et Retry-After. Le
// seau est celui de la machine, pas de l'adresse : une autre machine, avec
// la même adresse, passe encore. Le temps rend des places.
func TestAgentSignal_IsRateLimitedPerMachine(t *testing.T) {
	server := newBench(t)
	clock := testNow
	server.handler.signalLimits.setClock(func() time.Time { return clock })
	first, firstKey := server.enroll(t, "vps-paris-1", remoteID)
	resp, session, _, cancel := server.openStream(t, first.ID, firstKey)
	defer resp.Body.Close()
	defer cancel()
	second, secondKey := server.enroll(t, "vps-lyon-1", "22222222-3333-4444-8555-666666666666")
	resp2, otherSession, _, cancel2 := server.openStream(t, second.ID, secondKey)
	defer resp2.Body.Close()
	defer cancel2()

	for i := range signalPerMachineBurst {
		if got := server.bareSignal(session); got.Code != http.StatusNoContent {
			t.Fatalf("signal %d: %d %s", i+1, got.Code, got.Body.String())
		}
	}
	got := server.bareSignal(session)
	if got.Code != http.StatusTooManyRequests || got.Header().Get("Retry-After") == "" || !strings.Contains(got.Body.String(), "too_many_requests") {
		t.Fatalf("over the burst: %d %v %s", got.Code, got.Header(), got.Body.String())
	}
	if got := server.bareSignal(otherSession); got.Code != http.StatusNoContent {
		t.Fatalf("another machine must not be throttled by the first: %d", got.Code)
	}
	clock = clock.Add(2 * time.Second)
	if got := server.bareSignal(session); got.Code != http.StatusNoContent {
		t.Fatalf("two seconds later one signal must pass: %d", got.Code)
	}
	if got := server.bareSignal(session); got.Code != http.StatusTooManyRequests {
		t.Fatalf("and only one: %d", got.Code)
	}
}

// Le débit se juge après la session : un jeton inconnu reste un 401, quel
// que soit son débit, et ne remplit pas de seau.
func TestAgentSignal_UnknownSession_IsNeverThrottledNorCounted(t *testing.T) {
	server := newBench(t)
	for range signalPerMachineBurst + 5 {
		if got := server.bareSignal("nope"); got.Code != http.StatusUnauthorized {
			t.Fatalf("unknown session: %d", got.Code)
		}
	}
	if size := server.handler.signalLimits.byMachine.Size(); size != 0 {
		t.Fatalf("buckets = %d", size)
	}
}
