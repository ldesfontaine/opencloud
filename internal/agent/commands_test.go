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

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/update"
)

type fakeLogs struct{ requests []container.LogRequest }

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

func (l *fakeLogs) OpenLogs(ctx context.Context, request container.LogRequest) (<-chan container.LogBatch, error) {
	l.requests = append(l.requests, request)
	batches := make(chan container.LogBatch, 4)
	batches <- container.LogBatch{Lines: []container.LogLine{{Text: "a"}}}
	if request.Follow {
		// Un suivi vit jusqu'à l'ordre d'arrêt.
		go func() {
			<-ctx.Done()
			close(batches)
		}()
		return batches, nil
	}
	batches <- container.LogBatch{Done: true}
	close(batches)
	return batches, nil
}

type postedLogs struct {
	mu      sync.Mutex
	batches map[string][]container.LogBatch
	wake    chan struct{}
}

func (p *postedLogs) handler(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer session-1" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var batch container.LogBatch
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

// Une borne large : elle ne sert qu'à ne pas bloquer la CI si le code est
// cassé ; un runner chargé la consomme à peine.
const waitLimit = 30 * time.Second

func (p *postedLogs) waitFor(t *testing.T, requestID string, count int) {
	t.Helper()
	deadline := time.After(waitLimit)
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
	posted := &postedLogs{batches: make(map[string][]container.LogBatch), wake: make(chan struct{}, 16)}
	server := httptest.NewServer(http.HandlerFunc(posted.handler))
	defer server.Close()
	client, err := NewClient(server.URL, "", "v", false)
	if err != nil {
		t.Fatal(err)
	}
	logs := &fakeLogs{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := newCommandRunner(ctx, client, "session-1", logs, &fakeProber{}, &fakeUpdater{}, nil, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	runner.handle(string(agentwire.KindLogs), `{"id":"one","container_id":"c1","tail":10}`)
	posted.waitFor(t, "one", 2)
	if logs.requests[0].Tail != 10 || logs.requests[0].Follow || !posted.batches["one"][1].Done {
		t.Fatalf("one = %+v %+v", logs.requests, posted.batches["one"])
	}

	// Le 404 passe avant l'arrêt de « two » : annuler un envoi encore en vol
	// ferme sa connexion, et un autre suivi qui la reprend au même instant
	// échoue lui aussi (« context canceled »).
	runner.handle(string(agentwire.KindLogs), `{"id":"gone","container_id":"c3","follow":true}`)
	posted.waitFor(t, "gone", 1)
	waitUntilClosed(t, runner, "gone", "a 404 stops the follow")

	runner.handle(string(agentwire.KindLogs), `{"id":"two","container_id":"c2","follow":true}`)
	posted.waitFor(t, "two", 1)
	runner.handle(string(agentwire.KindLogsStop), `{"id":"two"}`)
	waitUntilClosed(t, runner, "two", "two should be stopped")

	runner.handle("unknown", `{"id":"x"}`)
	runner.handle(string(agentwire.KindLogs), `not json`)
}

// waitUntilClosed attend que le suivi ait quitté le registre : c'est la
// goroutine du suivi qui l'en retire après le 404, à son heure.
func waitUntilClosed(t *testing.T, runner *commandRunner, requestID, failure string) {
	t.Helper()
	deadline := time.Now().Add(waitLimit)
	for isOpen(runner, requestID) {
		if time.Now().After(deadline) {
			t.Fatal(failure)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func isOpen(runner *commandRunner, requestID string) bool {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	_, open := runner.open[requestID]
	return open
}

// La commande « probes » remplace le jeu de l'agent ; un contenu illisible
// ne le vide pas, il est simplement ignoré.
func TestCommandRunner_AssignsTheProbeSet(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prober := &fakeProber{}
	updater := &fakeUpdater{}
	runner := newCommandRunner(ctx, nil, "session-1", &fakeLogs{}, prober, updater, nil, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	payload, err := json.Marshal(probe.Assignment{Probes: []probe.Task{{ID: "p1", Kind: probe.KindHTTP, Target: "https://a.fr/", IntervalSeconds: 60}}})
	if err != nil {
		t.Fatal(err)
	}
	runner.handle(string(agentwire.KindProbes), string(payload))
	if assignment := prober.last(); len(assignment.Probes) != 1 || assignment.Probes[0].ID != "p1" {
		t.Fatalf("le jeu reçu n'est pas transmis: %+v", assignment)
	}

	runner.handle(string(agentwire.KindProbes), "{pas du json")
	if len(prober.assignments) != 1 {
		t.Fatalf("un contenu illisible a été transmis: %+v", prober.assignments)
	}

	// La commande « image_checks » règle les exclusions de la même façon.
	runner.handle(string(agentwire.KindImages), `{"excluded":["portfolio:latest"],"now":true}`)
	runner.handle(string(agentwire.KindImages), "{pas du json")
	if len(updater.assignments) != 1 || !updater.assignments[0].Now || updater.assignments[0].Excluded[0] != "portfolio:latest" {
		t.Fatalf("exclusions reçues: %+v", updater.assignments)
	}
}
