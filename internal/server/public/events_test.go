package public

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/status"
)

// Le direct public ne porte que « status », sur son propre bus.
func TestPublicEvents_StreamsStatusThenReconnects(t *testing.T) {
	server := newBench(t)
	next, _, _ := server.openLive(t, "198.51.100.7:5000", "")
	if got := next(); got != "connected" {
		t.Fatalf("first public event %q", got)
	}
	if _, err := server.status.CreateComponent(context.Background(), status.ComponentDefinition{Name: "Site web"}); err != nil {
		t.Fatal(err)
	}
	if got := next(); got != "status" {
		t.Fatalf("public event %q, want status", got)
	}
	if server.bus.Count() != 1 {
		t.Fatalf("subscribers %d", server.bus.Count())
	}
	again, _, _ := server.openLive(t, "198.51.100.8:5000", "4")
	if got := again(); got != "reconnected" {
		t.Fatalf("after Last-Event-ID: %q", got)
	}
}

// Le direct public a son propre plafond, qui ne compte que ses visiteurs.
func TestPublicEvents_RefusesBeyondItsCap(t *testing.T) {
	server := newBench(t)
	extra := server.bus.Subscribe()
	defer extra.Close()
	for range maxPublicSubscribers - 1 {
		server.handler.tabs.Take()
	}
	next, _, _ := server.openLive(t, "198.51.100.7:5000", "")
	if got := next(); got != "connected" {
		t.Fatalf("first event %q", got)
	}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/statut/api/events", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("%d %s", recorder.Code, recorder.Body.String())
	}
}

func TestPublicEvents_OneSourceCannotHoldMoreThanItsShare(t *testing.T) {
	server := newBench(t)
	holder := "198.51.100.7:5000"
	var cancels []context.CancelFunc
	for i := range maxPublicStreamsPerSource {
		_, response, cancel := server.openLive(t, holder, "")
		if response.StatusCode != http.StatusOK {
			t.Fatalf("stream %d: status %d", i+1, response.StatusCode)
		}
		cancels = append(cancels, cancel)
	}
	_, response, _ := server.openLive(t, holder, "")
	if response.StatusCode != http.StatusTooManyRequests || response.Header.Get("Retry-After") == "" {
		t.Fatalf("beyond the share: status %d, Retry-After %q", response.StatusCode, response.Header.Get("Retry-After"))
	}
	if taken := server.handler.tabs.Taken(); taken != maxPublicStreamsPerSource {
		t.Fatalf("the refused stream took a place of the global pool: %d taken", taken)
	}

	// Une autre source passe.
	if _, other, _ := server.openLive(t, "203.0.113.50:5000", ""); other.StatusCode != http.StatusOK {
		t.Fatalf("another source: status %d", other.StatusCode)
	}

	// Une place rendue se reprend (le seau d'ouverture est rempli entre-temps).
	cancels[0]()
	waitFor(t, func() bool { return server.handler.tabs.Taken() == maxPublicStreamsPerSource })
	server.handler.limits.setClock(func() time.Time { return testNow.Add(time.Minute) })
	if _, again, _ := server.openLive(t, holder, ""); again.StatusCode != http.StatusOK {
		t.Fatalf("after a stream ended: status %d", again.StatusCode)
	}
}

func TestPublicEvents_ShareIsPerIPv6SixtyFour(t *testing.T) {
	server := newBench(t)
	for i := range maxPublicStreamsPerSource {
		peer := "[2001:db8:5:5::" + string(rune('1'+i)) + "]:5000"
		if _, response, _ := server.openLive(t, peer, ""); response.StatusCode != http.StatusOK {
			t.Fatalf("stream %d: status %d", i+1, response.StatusCode)
		}
	}
	if _, response, _ := server.openLive(t, "[2001:db8:5:5:ffff::9]:5000", ""); response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("same /64: status %d, want 429", response.StatusCode)
	}
	if _, response, _ := server.openLive(t, "[2001:db8:5:6::1]:5000", ""); response.StatusCode != http.StatusOK {
		t.Fatalf("another /64: status %d", response.StatusCode)
	}
}

// Un flux public finit de lui-même à l'échéance, proprement : la place
// retourne au pool et le navigateur se reconnecte.
func TestPublicEvents_StreamEndsAfterItsMaximumLifetime(t *testing.T) {
	server := newBench(t)
	server.handler.streamLifetime = 300 * time.Millisecond
	_, response, _ := server.openLive(t, "198.51.100.7:5000", "")
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
	waitFor(t, func() bool { return server.handler.tabs.Taken() == 0 })
	if held := server.handler.tabsBySource.Sources(); held != 0 {
		t.Fatalf("%d sources still hold a share", held)
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
