package server

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/server/servertest"
)

// Les routes de l'agent se testent dans agentapi ; ici, de bout en bout,
// qu'elles sont montées, derrière la même chaîne, sur l'adresse que la
// racine résout.

func parsePrefixes(t *testing.T, cidrs ...string) []netip.Prefix {
	t.Helper()
	var prefixes []netip.Prefix
	for _, cidr := range cidrs {
		prefixes = append(prefixes, netip.MustParsePrefix(cidr))
	}
	return prefixes
}

// openStream ouvre le flux d'un agent à travers tout le serveur : montage,
// chaîne de middlewares et en-têtes compris.
func openStream(t *testing.T, server *testServer, machineID string, private ed25519.PrivateKey) (*http.Response, string, *bufio.Reader, context.CancelFunc) {
	t.Helper()
	return servertest.OpenStream(t, server.Server, machineID, private)
}

func TestAgentStream_OpensASessionThatSignalsAndCloses(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	resp, session, _, cancel := openStream(t, server, enrolled.ID, private)
	defer resp.Body.Close()
	defer cancel()

	status, _ := server.machines.Get(context.Background(), remoteID)
	if !status.Online || status.Agent.Version != "v0.0.2" {
		t.Fatalf("after stream: %+v", status)
	}
	signal := httptest.NewRequest(http.MethodPost, "/agent/signal", nil)
	signal.Header.Set("Authorization", "Bearer "+session)
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, signal)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("signal: %d %s", recorder.Code, recorder.Body.String())
	}
	bogus := httptest.NewRequest(http.MethodPost, "/agent/signal", nil)
	bogus.Header.Set("Authorization", "Bearer nope")
	recorder = httptest.NewRecorder()
	server.ServeHTTP(recorder, bogus)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("bogus signal: %d", recorder.Code)
	}

	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if status, _ := server.machines.Get(context.Background(), remoteID); !status.Online {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("machine still online after the stream was cut")
}

// Le limiteur de l'ouverture du flux compte l'adresse que la racine résout :
// derrière un mandataire de confiance, une autre adresse transmise passe
// encore. Les bornes elles-mêmes se testent dans agentapi.
func TestAgentStream_IsLimitedOnTheResolvedAddress(t *testing.T) {
	server := newTestServer(t)
	_, private := server.enroll(t, "vps-paris-1", remoteID)
	unknown := "11111111-1111-4111-8111-111111111111"
	attempt := func(forwardedFor string) *httptest.ResponseRecorder {
		request := servertest.SignedStreamRequest(t, "http://example.com", unknown, private, servertest.NewNonce(t))
		request.RemoteAddr = "192.0.2.1:1234"
		if forwardedFor != "" {
			request.Header.Set("X-Forwarded-For", forwardedFor)
		}
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		return recorder
	}
	refused := false
	for range 100 {
		got := attempt("")
		if got.Code == http.StatusTooManyRequests && got.Header().Get("Retry-After") != "" {
			refused = true
			break
		}
		if got.Code != http.StatusNotFound {
			t.Fatalf("under the burst: %d %s", got.Code, got.Body.String())
		}
	}
	if !refused {
		t.Fatal("the stream is never rate limited")
	}
	server.trustedProxies = parsePrefixes(t, "192.0.2.0/24")
	if got := attempt("203.0.113.50"); got.Code != http.StatusNotFound {
		t.Fatalf("another source: %d", got.Code)
	}
}

// Un lot de plus de lignes qu'un agent n'en envoie est refusé en 413 sans
// fermer la requête : le lot suivant, dans la limite, passe. De bout en
// bout : le navigateur ouvre le suivi, l'agent livre.
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
	requestID := jsonID(t, readCommand(t, agentStream, string(agentwire.KindLogs)))

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
