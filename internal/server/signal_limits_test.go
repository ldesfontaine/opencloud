package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
)

func (ts *testServer) bareSignal(session string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/agent/signal", nil)
	request.Header.Set("Authorization", "Bearer "+session)
	recorder := httptest.NewRecorder()
	ts.ServeHTTP(recorder, request)
	return recorder
}

// Une machine qui signale en rafale est freinée : 429 et Retry-After. Le
// seau est celui de la machine, pas de l'adresse : une autre machine, avec
// la même adresse, passe encore. Le temps rend des places.
func TestAgentSignal_IsRateLimitedPerMachine(t *testing.T) {
	server := newTestServer(t)
	clock := testNow
	server.signalLimits.setClock(func() time.Time { return clock })
	first, firstKey := server.enroll(t, "vps-paris-1", remoteID)
	resp, session, _, cancel := openStream(t, server, first.ID, firstKey)
	defer resp.Body.Close()
	defer cancel()
	second, secondKey := server.enroll(t, "vps-lyon-1", "22222222-3333-4444-8555-666666666666")
	resp2, otherSession, _, cancel2 := openStream(t, server, second.ID, secondKey)
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
	server := newTestServer(t)
	for range signalPerMachineBurst + 5 {
		if got := server.bareSignal("nope"); got.Code != http.StatusUnauthorized {
			t.Fatalf("unknown session: %d", got.Code)
		}
	}
	if size := server.signalLimits.byMachine.Size(); size != 0 {
		t.Fatalf("buckets = %d", size)
	}
}

// Un lot de plus de lignes qu'un agent n'en envoie est refusé en 413 sans
// fermer la requête : le lot suivant, dans la limite, passe.
func TestAgentLogs_BatchOverTheLimit_IsRefusedWith413(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	resp, session, agentStream, cancel := openStream(t, server, enrolled.ID, private)
	defer resp.Body.Close()
	defer cancel()
	containerID := server.seedContainers(t)

	live := httptest.NewServer(server.Server)
	defer live.Close()
	request, _ := http.NewRequest(http.MethodGet, live.URL+"/api/containers/"+containerID+"/logs/stream", nil)
	browser, err := live.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer browser.Body.Close()
	requestID := jsonID(t, readCommand(t, agentStream, container.CommandLogs))

	tooMany := container.LogBatch{Lines: make([]container.LogLine, container.MaxLinesPerBatch+1)}
	got := server.agentPost(session, "/agent/logs/"+requestID, tooMany)
	if got.Code != http.StatusRequestEntityTooLarge || !strings.Contains(got.Body.String(), "batch_too_large") {
		t.Fatalf("65 lines: %d %s", got.Code, got.Body.String())
	}
	// Une ligne trop longue est coupée, pas refusée : un agent installé en
	// envoie, le refus arrêterait son suivi.
	tooLong := container.LogBatch{Lines: []container.LogLine{{Stream: "stdout", Text: strings.Repeat("a", container.MaxLogLineBytes+1)}}}
	if got := server.agentPost(session, "/agent/logs/"+requestID, tooLong); got.Code != http.StatusNoContent {
		t.Fatalf("long line: %d %s", got.Code, got.Body.String())
	}
	ok := container.LogBatch{Lines: make([]container.LogLine, container.MaxLinesPerBatch)}
	if got := server.agentPost(session, "/agent/logs/"+requestID, ok); got.Code != http.StatusNoContent {
		t.Fatalf("64 lines: %d %s", got.Code, got.Body.String())
	}
}
