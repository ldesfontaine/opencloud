package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/service"
	"github.com/ldesfontaine/opencloud/internal/update"
)

type fakeLogs struct{ requests []service.LogRequest }

// fakeUpdater note les exclusions que le serveur pousse.
type fakeUpdater struct {
	mu          sync.Mutex
	assignments []update.Assignment
}

func (u *fakeUpdater) Assign(assignment update.Assignment) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.assignments = append(u.assignments, assignment)
}

// fakeProber note les jeux de sondes que le serveur pousse.
type fakeProber struct {
	mu          sync.Mutex
	assignments []probe.Assignment
}

func (p *fakeProber) Assign(assignment probe.Assignment) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.assignments = append(p.assignments, assignment)
}

func (p *fakeProber) last() probe.Assignment {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.assignments) == 0 {
		return probe.Assignment{}
	}
	return p.assignments[len(p.assignments)-1]
}

func (l *fakeLogs) OpenLogs(ctx context.Context, request service.LogRequest) (<-chan service.LogBatch, error) {
	l.requests = append(l.requests, request)
	batches := make(chan service.LogBatch, 4)
	batches <- service.LogBatch{Lines: []service.LogLine{{Text: "a"}}}
	if request.Follow {
		// Un suivi vit jusqu'à l'ordre d'arrêt.
		go func() {
			<-ctx.Done()
			close(batches)
		}()
		return batches, nil
	}
	batches <- service.LogBatch{Done: true}
	close(batches)
	return batches, nil
}

type postedLogs struct {
	mu      sync.Mutex
	batches map[string][]service.LogBatch
	wake    chan struct{}
}

func (p *postedLogs) handler(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer session-1" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var batch service.LogBatch
	_ = json.NewDecoder(r.Body).Decode(&batch)
	requestID := strings.TrimPrefix(r.URL.Path, "/agent/logs/")
	p.mu.Lock()
	p.batches[requestID] = append(p.batches[requestID], batch)
	p.mu.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}
	if requestID == "gone" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (p *postedLogs) count(requestID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.batches[requestID])
}

func (p *postedLogs) waitFor(t *testing.T, requestID string, count int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for p.count(requestID) < count {
		select {
		case <-p.wake:
		case <-deadline:
			t.Fatalf("%s: %d batches, want %d", requestID, p.count(requestID), count)
		}
	}
}

// Une commande « logs » ouvre un suivi et livre chaque lot ; « logs_stop »
// l'arrête ; un serveur qui répond 404 l'arrête aussi.
func TestCommandRunner_ServesLogsUntilStopped(t *testing.T) {
	posted := &postedLogs{batches: make(map[string][]service.LogBatch), wake: make(chan struct{}, 16)}
	server := httptest.NewServer(http.HandlerFunc(posted.handler))
	defer server.Close()
	client, err := NewClient(server.URL, "", "v", false)
	if err != nil {
		t.Fatal(err)
	}
	logs := &fakeLogs{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := newCommandRunner(ctx, client, "session-1", logs, &fakeProber{}, &fakeUpdater{}, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	runner.handle(service.CommandLogs, `{"id":"one","container_id":"c1","tail":10}`)
	posted.waitFor(t, "one", 2)
	if logs.requests[0].Tail != 10 || logs.requests[0].Follow || !posted.batches["one"][1].Done {
		t.Fatalf("one = %+v %+v", logs.requests, posted.batches["one"])
	}

	runner.handle(service.CommandLogs, `{"id":"two","container_id":"c2","follow":true}`)
	posted.waitFor(t, "two", 1)
	runner.handle(service.CommandLogsStop, `{"id":"two"}`)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		runner.mu.Lock()
		_, open := runner.open["two"]
		runner.mu.Unlock()
		if !open {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	runner.mu.Lock()
	if _, open := runner.open["two"]; open {
		t.Fatal("two should be stopped")
	}
	runner.mu.Unlock()

	runner.handle(service.CommandLogs, `{"id":"gone","container_id":"c3","follow":true}`)
	posted.waitFor(t, "gone", 1)
	time.Sleep(20 * time.Millisecond)
	runner.mu.Lock()
	_, open := runner.open["gone"]
	runner.mu.Unlock()
	if open {
		t.Fatal("a 404 stops the follow")
	}
	runner.handle("unknown", `{"id":"x"}`)
	runner.handle(service.CommandLogs, `not json`)
}

// La commande « probes » remplace le jeu de l'agent ; un contenu illisible
// ne le vide pas, il est simplement ignoré.
func TestCommandRunner_AssignsTheProbeSet(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prober := &fakeProber{}
	updater := &fakeUpdater{}
	runner := newCommandRunner(ctx, nil, "session-1", &fakeLogs{}, prober, updater, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	payload, err := json.Marshal(probe.Assignment{Probes: []probe.Task{{ID: "p1", Kind: probe.KindHTTP, Target: "https://a.fr/", IntervalSeconds: 60}}})
	if err != nil {
		t.Fatal(err)
	}
	runner.handle(probe.CommandProbes, string(payload))
	if assignment := prober.last(); len(assignment.Probes) != 1 || assignment.Probes[0].ID != "p1" {
		t.Fatalf("le jeu reçu n'est pas transmis: %+v", assignment)
	}

	runner.handle(probe.CommandProbes, "{pas du json")
	if len(prober.assignments) != 1 {
		t.Fatalf("un contenu illisible a été transmis: %+v", prober.assignments)
	}

	// La commande « image_checks » règle les exclusions de la même façon.
	runner.handle(update.CommandImages, `{"excluded":["portfolio:latest"],"now":true}`)
	runner.handle(update.CommandImages, "{pas du json")
	if len(updater.assignments) != 1 || !updater.assignments[0].Now || updater.assignments[0].Excluded[0] != "portfolio:latest" {
		t.Fatalf("exclusions reçues: %+v", updater.assignments)
	}
}
