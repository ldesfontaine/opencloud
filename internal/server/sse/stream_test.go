package sse

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/live"
)

const testPing = 20 * time.Millisecond

// open sert un flux sur un bus neuf et rend un lecteur de blocs : un bloc est
// un événement ou un commentaire, sans sa ligne vide de fin.
func open(t *testing.T, bus *live.Bus, lapsed func() bool, lastEventID string) (next func() string, cancel context.CancelFunc) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stream(w, r, bus.Subscribe(), lapsed, testPing)
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(context.Background())
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if lastEventID != "" {
		request.Header.Set("Last-Event-ID", lastEventID)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if response.Header.Get("Content-Type") != "text/event-stream" || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("headers %v", response.Header)
	}
	t.Cleanup(func() { cancel(); _ = response.Body.Close() })
	reader := bufio.NewReader(response.Body)
	next = func() string {
		var lines []string
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return "EOF"
			}
			line = strings.TrimSuffix(line, "\n")
			if line == "" {
				return strings.Join(lines, "\n")
			}
			lines = append(lines, line)
		}
	}
	return next, cancel
}

// nextEvent saute les pings : un test d'événements ne dépend pas de l'horloge.
func nextEvent(next func() string) string {
	for {
		if block := next(); block != ": ping" {
			return block
		}
	}
}

func TestStream_FirstEventCarriesTheRetry_ThenTopicsAreNumbered(t *testing.T) {
	bus := live.New()
	next, _ := open(t, bus, nil, "")
	if got, want := next(), "retry: 5000\nid: 1\nevent: connected\ndata: "; got != want {
		t.Fatalf("first event %q, want %q", got, want)
	}
	bus.Publish(live.TopicStatus)
	if got, want := nextEvent(next), "id: 2\nevent: status\ndata: "; got != want {
		t.Fatalf("second event %q, want %q", got, want)
	}
}

func TestStream_ALastEventID_IsAnsweredWithReconnected(t *testing.T) {
	next, _ := open(t, live.New(), nil, "7")
	if got, want := next(), "retry: 5000\nid: 1\nevent: reconnected\ndata: "; got != want {
		t.Fatalf("first event %q, want %q", got, want)
	}
}

func TestStream_PingsKeepTheConnectionOpen(t *testing.T) {
	next, _ := open(t, live.New(), nil, "")
	next()
	if got := next(); got != ": ping" {
		t.Fatalf("after the first event %q, want a ping", got)
	}
}

func TestStream_ClosesWhenTheAuthorizationLapses(t *testing.T) {
	next, _ := open(t, live.New(), func() bool { return true }, "")
	next()
	if got := next(); got != "EOF" {
		t.Fatalf("after the first ping %q, want the end of the stream", got)
	}
}

func TestStream_UnsubscribesWhenTheClientLeaves(t *testing.T) {
	bus := live.New()
	next, cancel := open(t, bus, nil, "")
	next()
	if bus.Count() != 1 {
		t.Fatalf("subscribers %d", bus.Count())
	}
	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for bus.Count() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the subscription outlived the client")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
