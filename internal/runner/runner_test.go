package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/transport"
	"github.com/ldesfontaine/opencloud/migrations"
)

// Le faux catalogue : une seule définition, une préparation déterministe.
type fakeCatalog struct {
	definition catalog.Definition
	script     []byte
	files      []catalog.File
	err        error
}

func newFakeCatalog() *fakeCatalog {
	return &fakeCatalog{
		definition: catalog.Definition{
			Kind:       catalog.KindDiagnostiquer,
			Label:      "Diagnostiquer",
			Summary:    "Lecture seule : services, disque, horloge, ports.",
			Scope:      catalog.ScopeMachine,
			Place:      catalog.PlaceTarget,
			Reversible: true,
			Interrupts: false,
			Timeout:    10 * time.Minute,
		},
		script: []byte("#!/bin/bash\nexit 0\n"),
	}
}

func (c *fakeCatalog) Definitions() []catalog.Definition {
	return []catalog.Definition{c.definition}
}

func (c *fakeCatalog) Lookup(kind catalog.Kind) (catalog.Definition, bool) {
	if kind != c.definition.Kind {
		return catalog.Definition{}, false
	}
	return c.definition, true
}

func (c *fakeCatalog) Prepare(kind catalog.Kind, params map[string]string) (catalog.Prepared, error) {
	if c.err != nil {
		return catalog.Prepared{}, c.err
	}
	digest := sha256.Sum256(c.script)
	return catalog.Prepared{
		Definition:   c.definition,
		Params:       params,
		Script:       c.script,
		ScriptDigest: "sha256:" + hex.EncodeToString(digest[:]),
		ParamsEnv:    []byte("OC_MACHINE=local\n"),
		Files:        c.files,
	}, nil
}

type putFile struct {
	name    string
	content []byte
	mode    fs.FileMode
}

// Le faux transport : lignes émises, issue choisie, injoignable N fois.
type fakeTransport struct {
	lines           []transport.Line
	outcome         transport.Outcome
	launchErr       error
	followErr       error
	unreachableLeft int
	followDelay     time.Duration
	gate            chan struct{}

	mu           sync.Mutex
	puts         []putFile
	launches     int
	follows      int
	fromCursors  []string
	active       int
	mostActive   int
	lastActionID string
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{
		lines: []transport.Line{
			{At: time.Unix(1, 0).UTC(), Text: "étape: disque", Cursor: "c1"},
			{At: time.Unix(2, 0).UTC(), Text: "résultat: rien à signaler", Cursor: "c2"},
		},
	}
}

func (t *fakeTransport) Put(_ context.Context, actionID, name string, content []byte, mode fs.FileMode) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastActionID = actionID
	t.puts = append(t.puts, putFile{name: name, content: content, mode: mode})
	return nil
}

func (t *fakeTransport) Launch(context.Context, string) error {
	t.mu.Lock()
	t.launches++
	t.mu.Unlock()
	return t.launchErr
}

func (t *fakeTransport) Follow(ctx context.Context, _ string, afterCursor string, emit func(transport.Line)) (transport.Outcome, error) {
	t.mu.Lock()
	t.follows++
	t.fromCursors = append(t.fromCursors, afterCursor)
	t.active++
	if t.active > t.mostActive {
		t.mostActive = t.active
	}
	unreachable := t.unreachableLeft > 0
	if unreachable {
		t.unreachableLeft--
	}
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		t.active--
		t.mu.Unlock()
	}()

	if t.gate != nil {
		select {
		case <-t.gate:
		case <-ctx.Done():
			return transport.Outcome{}, ctx.Err()
		}
	}
	if t.followDelay > 0 {
		select {
		case <-time.After(t.followDelay):
		case <-ctx.Done():
			return transport.Outcome{}, ctx.Err()
		}
	}
	if unreachable {
		return transport.Outcome{}, transport.ErrUnreachable
	}
	if t.followErr != nil {
		return transport.Outcome{}, t.followErr
	}

	for _, line := range t.linesAfter(afterCursor) {
		emit(line)
	}
	return t.outcome, nil
}

func (t *fakeTransport) linesAfter(cursor string) []transport.Line {
	if cursor == "" {
		return t.lines
	}
	for index, line := range t.lines {
		if line.Cursor == cursor {
			return t.lines[index+1:]
		}
	}
	return t.lines
}

func (t *fakeTransport) Probe(context.Context) error { return nil }

func (t *fakeTransport) putNames() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var names []string
	for _, put := range t.puts {
		names = append(names, put.name)
	}
	return names
}

func (t *fakeTransport) putRecords() []putFile {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]putFile(nil), t.puts...)
}

func (t *fakeTransport) cursors() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.fromCursors...)
}

func (t *fakeTransport) counts() (launches, follows int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.launches, t.follows
}

type fakeTransports struct {
	transport transport.Transport
	err       error
}

func (f *fakeTransports) For(context.Context, store.Machine) (transport.Transport, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.transport, nil
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })

	database, err := store.Open(context.Background(), root, migrations.Files, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

// newTestRunner : mêmes règles qu'en production, mais sans attendre — les
// réessais sont immédiats.
func newTestRunner(t *testing.T, database *store.Store, actionCatalog Catalog, transports Transports) *Runner {
	t.Helper()
	runner := New(database, actionCatalog, transports, slog.New(slog.DiscardHandler))
	runner.retryDelays = []time.Duration{time.Millisecond}
	runner.followGrace = 0
	t.Cleanup(runner.Close)
	return runner
}

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func waitForState(t *testing.T, database *store.Store, actionID string, wanted store.ActionState) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		action, err := database.Action(context.Background(), actionID)
		if err != nil {
			t.Fatal(err)
		}
		if action.State == wanted {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("l'action %s n'est pas passée à %s", actionID, wanted)
}

func waitForConclusion(t *testing.T, database *store.Store, actionID string) store.Action {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		action, err := database.Action(context.Background(), actionID)
		if err != nil {
			t.Fatal(err)
		}
		if action.State != store.StatePrepared && action.State != store.StateRunning {
			return action
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("l'action %s n'a pas conclu à temps", actionID)
	return store.Action{}
}
