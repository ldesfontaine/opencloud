package server

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

// Ouvre le direct d'un onglet et rend un lecteur d'événements nommés.
func openEvents(t *testing.T, server *testServer, lastEventID string) (func() string, context.CancelFunc) {
	t.Helper()
	return openLive(t, server, "/api/events", lastEventID)
}

// openLive ouvre un flux SSE du serveur, celui de l'administration ou
// le public, et rend un lecteur d'événements nommés.
func openLive(t *testing.T, server *testServer, path, lastEventID string) (func() string, context.CancelFunc) {
	t.Helper()
	liveServer := httptest.NewServer(server.Server)
	t.Cleanup(liveServer.Close)
	ctx, cancel := context.WithCancel(context.Background())
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, liveServer.URL+path, nil)
	if lastEventID != "" {
		request.Header.Set("Last-Event-ID", lastEventID)
	}
	resp, err := liveServer.Client().Do(request)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		cancel()
		t.Fatalf("status %d, type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	reader := bufio.NewReader(resp.Body)
	next := func() string {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return "EOF"
			}
			if name, ok := strings.CutPrefix(line, "event: "); ok {
				return strings.TrimSpace(name)
			}
		}
	}
	t.Cleanup(func() { cancel(); resp.Body.Close() })
	return next, cancel
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition never met")
}

func TestEvents_StreamsTopicsThenUnsubscribesWhenTheTabCloses(t *testing.T) {
	server := newTestServer(t)
	next, cancel := openEvents(t, server, "")
	if got := next(); got != "connected" {
		t.Fatalf("first event %q", got)
	}
	if server.bus.Count() != 1 {
		t.Fatalf("subscribers %d", server.bus.Count())
	}
	server.bus.Publish(live.TopicJobs)
	if got := next(); got != "jobs" {
		t.Fatalf("event %q, want jobs", got)
	}
	cancel()
	waitFor(t, func() bool { return server.bus.Count() == 0 })
}

// Le navigateur revient avec Last-Event-ID : on lui dit de tout relire.
func TestEvents_SaysReconnectedWhenTheBrowserComesBack(t *testing.T) {
	server := newTestServer(t)
	next, _ := openEvents(t, server, "7")
	if got := next(); got != "reconnected" {
		t.Fatalf("first event %q", got)
	}
}

// Ce qui change vraiment arrive à l'onglet : une machine qui ouvre son
// flux, un ping reçu.
func TestEvents_RealChangesReachTheTab(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	job := server.createJob(t, "job")
	next, _ := openEvents(t, server, "")
	next()
	resp, _, _, cancelStream := openStream(t, server, enrolled.ID, private)
	defer resp.Body.Close()
	defer cancelStream()
	if got := next(); got != "machines" {
		t.Fatalf("after connect: %q", got)
	}
	if got := ping(server.Server, http.MethodGet, "/ping/"+job.Token, "", ""); got.Code != http.StatusOK {
		t.Fatalf("ping: %d", got.Code)
	}
	if got := next(); got != "jobs" {
		t.Fatalf("after ping: %q", got)
	}
	if err := server.heartbeats.Pause(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if got := next(); got != "jobs" {
		t.Fatalf("after pause: %q", got)
	}
}

func TestEvents_RefusesBeyondTheCap(t *testing.T) {
	server := newTestServer(t)
	for range maxEventSubscribers {
		server.bus.Subscribe()
	}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/events", nil))
	if recorder.Code != http.StatusServiceUnavailable || errorCode(t, recorder) != codeBusy {
		t.Fatalf("%d %s", recorder.Code, recorder.Body.String())
	}
}
