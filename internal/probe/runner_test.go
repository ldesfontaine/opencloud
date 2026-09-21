package probe

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

type collectingSink struct {
	mu      sync.Mutex
	results []Result
	arrived chan struct{}
}

func newSink() *collectingSink {
	return &collectingSink{arrived: make(chan struct{}, 64)}
}

func (s *collectingSink) Deliver(report Report) {
	s.mu.Lock()
	s.results = append(s.results, report.Results...)
	s.mu.Unlock()
	select {
	case s.arrived <- struct{}{}:
	default:
	}
}

func (s *collectingSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.results)
}

func (s *collectingSink) waitFor(t *testing.T, wanted int) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for s.count() < wanted {
		select {
		case <-s.arrived:
		case <-deadline:
			t.Fatalf("attendu %d essais, obtenu %d", wanted, s.count())
		}
	}
}

func testRunner(t *testing.T, sink Sink) (*Runner, context.CancelFunc) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	runner := NewRunner(sink, NewChecker(nil), logger)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return runner, cancel
}

// Une sonde part tout de suite : l'opérateur qui vient de la créer voit ce
// qu'elle voit sans attendre son premier intervalle.
func TestRunner_ProbesAtOnceWhenAssigned(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer server.Close()
	sink := newSink()
	runner, _ := testRunner(t, sink)

	runner.Assign(Assignment{Probes: []Task{httpTask(server.URL)}})
	sink.waitFor(t, 1)
	if runner.Count() != 1 {
		t.Fatalf("attendu une sonde en cours, obtenu %d", runner.Count())
	}
}

// Le jeu remplace le précédent : ce qui n'y est plus s'arrête.
func TestRunner_AssignReplacesTheWholeSet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer server.Close()
	sink := newSink()
	runner, _ := testRunner(t, sink)

	first := httpTask(server.URL)
	second := httpTask(server.URL)
	second.ID = "p2"
	runner.Assign(Assignment{Probes: []Task{first, second}})
	sink.waitFor(t, 2)

	runner.Assign(Assignment{Probes: []Task{first}})
	if runner.Count() != 1 {
		t.Fatalf("attendu une sonde après le retrait, obtenu %d", runner.Count())
	}

	runner.Assign(Assignment{})
	if runner.Count() != 0 {
		t.Fatalf("un jeu vide devait tout arrêter, %d en cours", runner.Count())
	}
}

// Une sonde inchangée garde sa goroutine, donc son horloge : changer une
// autre sonde ne relance pas tout le monde.
func TestRunner_UnchangedTaskIsNotRestarted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer server.Close()
	sink := newSink()
	runner, _ := testRunner(t, sink)

	kept := httpTask(server.URL)
	runner.Assign(Assignment{Probes: []Task{kept}})
	sink.waitFor(t, 1)

	added := httpTask(server.URL)
	added.ID = "p2"
	runner.Assign(Assignment{Probes: []Task{kept, added}})
	sink.waitFor(t, 2)
	if got := sink.count(); got != 2 {
		t.Fatalf("la sonde inchangée a resondé: %d essais pour deux sondes", got)
	}
}

// Une sonde dont la cible change repart : sinon elle continuerait de
// sonder l'ancienne.
func TestRunner_ChangedTaskIsRestarted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer server.Close()
	sink := newSink()
	runner, _ := testRunner(t, sink)

	task := httpTask(server.URL)
	runner.Assign(Assignment{Probes: []Task{task}})
	sink.waitFor(t, 1)

	task.ExpectedStatus = "204"
	runner.Assign(Assignment{Probes: []Task{task}})
	sink.waitFor(t, 2)
	if runner.Count() != 1 {
		t.Fatalf("attendu une seule goroutine après la reconfiguration, obtenu %d", runner.Count())
	}
}

// Un jeu reçu avant le démarrage attend celui-ci, il ne se perd pas.
func TestRunner_AssignBeforeRunIsKept(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer server.Close()
	sink := newSink()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	runner := NewRunner(sink, NewChecker(nil), logger)
	runner.Assign(Assignment{Probes: []Task{httpTask(server.URL)}})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.Run(ctx)
	}()
	sink.waitFor(t, 1)
	cancel()
	<-done
	if runner.Count() != 0 {
		t.Fatalf("l'arrêt devait tout fermer, %d en cours", runner.Count())
	}
}
