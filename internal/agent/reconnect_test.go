package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/lang"
)

// fakeServer joue le serveur openCloud du point de vue de l'agent : défi,
// flux, signal. Chaque route répond ce que le test lui dit.
type fakeServer struct {
	mu             sync.Mutex
	streamRefusal  string
	signalRefusals []string
	streams        []time.Time
	refusedSignals []time.Time
	acceptedSignal int
}

func (f *fakeServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /agent/challenge", func(w http.ResponseWriter, r *http.Request) {
		nonce := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
		_ = json.NewEncoder(w).Encode(agentwire.ChallengeResponse{Nonce: nonce})
	})
	mux.HandleFunc("GET /agent/stream", f.stream)
	mux.HandleFunc("POST /agent/signal", f.signal)
	return mux
}

func (f *fakeServer) stream(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.streams = append(f.streams, time.Now())
	count := len(f.streams)
	refusal := f.streamRefusal
	f.mu.Unlock()
	if refusal != "" {
		writeRefusal(w, refusal)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "event: session\ndata: session-%d\n\n", count)
	http.NewResponseController(w).Flush()
	<-r.Context().Done()
}

func (f *fakeServer) signal(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.signalRefusals) > 0 {
		refusal := f.signalRefusals[0]
		f.signalRefusals = f.signalRefusals[1:]
		f.refusedSignals = append(f.refusedSignals, time.Now())
		writeRefusal(w, refusal)
		return
	}
	f.acceptedSignal++
	w.WriteHeader(http.StatusNoContent)
}

func writeRefusal(w http.ResponseWriter, code string) {
	status := http.StatusUnauthorized
	switch code {
	case codeMachineNotFound:
		status = http.StatusNotFound
	case "too_many_requests":
		status = http.StatusTooManyRequests
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func (f *fakeServer) snapshot() (streams, refused []time.Time, accepted int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Time(nil), f.streams...), append([]time.Time(nil), f.refusedSignals...), f.acceptedSignal
}

// startAgent tient le flux contre le faux serveur, comme Run une fois
// l'identité chargée, et rend ce que keepConnected a rendu.
func startAgent(t *testing.T, ctx context.Context, server *fakeServer, logs *bytes.Buffer) <-chan error {
	t.Helper()
	httpServer := httptest.NewServer(server.handler())
	t.Cleanup(httpServer.Close)
	identity, err := NewIdentity(httpServer.URL, "", lang.French, nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(httpServer.URL, "", "v0.0.1", false)
	if err != nil {
		t.Fatal(err)
	}
	catalogs, err := lang.Load()
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{SignalInterval: 30 * time.Millisecond, Logger: slog.New(slog.NewTextHandler(logs, nil))}
	waiting := &pending{readings: &buffer{}, reports: &reports{}, results: &results{}, images: &images{}, actions: newActionResults()}
	done := make(chan error, 1)
	go func() { done <- keepConnected(ctx, client, identity, waiting, catalogs.For(lang.French), opts) }()
	return done
}

func waitUntil(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("never happened: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Le serveur a redémarré entre deux signaux : il ne connaît plus la
// session. L'agent rouvre un flux tout de suite, sans attendre le délai.
func TestKeepConnected_ExpiredSession_ReconnectsAtOnce(t *testing.T) {
	server := &fakeServer{signalRefusals: []string{codeNoSession}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startAgent(t, ctx, server, &bytes.Buffer{})

	waitUntil(t, "a signal accepted on a second stream", func() bool {
		streams, _, accepted := server.snapshot()
		return len(streams) >= 2 && accepted > 0
	})
	streams, refused, _ := server.snapshot()
	if gap := streams[1].Sub(refused[0]); gap >= minBackoff/2 {
		t.Fatalf("reconnected %v after the refusal, not at once", gap)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("agent stopped with %v", err)
	}
}

// Le serveur freine les signaux de cette machine (429) : l'agent garde son
// flux, ne se reconnecte pas, et le signal suivant passe.
func TestKeepConnected_ThrottledSignals_KeepTheStreamOpen(t *testing.T) {
	server := &fakeServer{signalRefusals: []string{"too_many_requests", "too_many_requests"}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startAgent(t, ctx, server, &bytes.Buffer{})

	waitUntil(t, "a signal accepted after the two throttled ones", func() bool {
		_, refused, accepted := server.snapshot()
		return len(refused) == 2 && accepted > 0
	})
	select {
	case err := <-done:
		t.Fatalf("agent stopped on a 429: %v", err)
	default:
	}
	if streams, _, _ := server.snapshot(); len(streams) != 1 {
		t.Fatalf("the agent reopened the stream %d times after a 429", len(streams))
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("agent stopped with %v", err)
	}
}

// Une horloge décalée de plus de 5 min : l'agent ne s'arrête pas, réessaie
// avec son délai et dit à l'opérateur, dans sa langue, de régler l'heure.
func TestKeepConnected_ClockSkew_KeepsRetryingAndSaysToFixTheClock(t *testing.T) {
	server := &fakeServer{streamRefusal: codeClockSkew}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logs := &bytes.Buffer{}
	done := startAgent(t, ctx, server, logs)

	waitUntil(t, "a second attempt", func() bool {
		streams, _, _ := server.snapshot()
		return len(streams) >= 2
	})
	select {
	case err := <-done:
		t.Fatalf("agent stopped on a clock skew: %v", err)
	default:
	}
	streams, _, _ := server.snapshot()
	if gap := streams[1].Sub(streams[0]); gap < minBackoff/2 {
		t.Fatalf("retried after %v, without waiting", gap)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("agent stopped with %v", err)
	}
	catalogs, _ := lang.Load()
	// Le journal est en JSON ou en texte : le message y est cité, guillemets échappés.
	advice := strings.ReplaceAll(catalogs.For(lang.French).Get("agent.clock_skew"), `"`, `\"`)
	if !strings.Contains(logs.String(), advice) {
		t.Fatalf("the log does not tell to fix the clock:\n%s", logs.String())
	}
}

// Un défi expiré, évincé ou perdu au redémarrage du serveur : l'agent n'a
// rien à se reprocher, il en redemande un et réessaie, sans s'arrêter.
func TestKeepConnected_ChallengeExpired_KeepsRetrying(t *testing.T) {
	server := &fakeServer{streamRefusal: codeChallengeExpired}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startAgent(t, ctx, server, &bytes.Buffer{})

	waitUntil(t, "a second attempt", func() bool {
		streams, _, _ := server.snapshot()
		return len(streams) >= 2
	})
	select {
	case err := <-done:
		t.Fatalf("agent stopped on an expired challenge: %v", err)
	default:
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("agent stopped with %v", err)
	}
}

// Une signature que le serveur ne reconnaît pas : la clé n'est plus celle
// enrôlée, l'agent s'arrête et le dit.
func TestKeepConnected_BadProof_Stops(t *testing.T) {
	server := &fakeServer{streamRefusal: codeBadProof}
	done := startAgent(t, context.Background(), server, &bytes.Buffer{})
	select {
	case err := <-done:
		if !errors.Is(err, ErrIdentityRefused) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("agent kept retrying a refused identity")
	}
}

// La machine a été retirée : réessayer ne sert à rien, l'agent s'arrête.
func TestKeepConnected_MachineNotFound_Stops(t *testing.T) {
	server := &fakeServer{streamRefusal: codeMachineNotFound}
	done := startAgent(t, context.Background(), server, &bytes.Buffer{})
	select {
	case err := <-done:
		if !errors.Is(err, ErrIdentityRefused) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("agent kept retrying a removed machine")
	}
}

func TestServerError_OnlyAnIdentityOrEnrollmentRefusalIsPermanent(t *testing.T) {
	cases := []struct {
		status    int
		code      string
		permanent bool
	}{
		{http.StatusNotFound, "machine_not_found", true},
		{http.StatusUnauthorized, "bad_proof", true},
		{http.StatusConflict, "token_consumed", true},
		{http.StatusConflict, "name_taken", true},
		{http.StatusUnauthorized, "challenge_expired", false},
		{http.StatusUnauthorized, "no_session", false},
		{http.StatusUnauthorized, "clock_skew", false},
		{http.StatusUnauthorized, "", false},
		{http.StatusNotFound, "", false},
		{http.StatusBadRequest, "bad_proof", false},
		{http.StatusInternalServerError, "internal", false},
		{http.StatusTooManyRequests, "too_many_requests", false},
	}
	for _, c := range cases {
		refused := &ServerError{Status: c.status, Code: c.code}
		if refused.Permanent() != c.permanent {
			t.Errorf("%d %q: permanent %v, want %v", c.status, c.code, refused.Permanent(), c.permanent)
		}
	}
}
