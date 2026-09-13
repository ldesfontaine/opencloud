package agent

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/settings"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/web"
)

// Un vrai serveur openCloud sur un port éphémère, et un vrai agent contre lui.
type bench struct {
	url      string
	machines *machine.Service
	stateDir *os.Root
	logger   *slog.Logger
}

func newBench(t *testing.T) *bench {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	serverRoot, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { serverRoot.Close() })
	db, err := store.Open(context.Background(), serverRoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	machines := machine.New(db, machine.NewSessions(), logger)
	handler, err := web.New(web.Options{Logger: logger, Version: "v0.0.1", Settings: settings.New(serverRoot), Machines: machines})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	agentRoot, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { agentRoot.Close() })
	return &bench{url: server.URL, machines: machines, stateDir: agentRoot, logger: logger}
}

func (b *bench) options(token string) Options {
	return Options{StateDir: b.stateDir, Server: b.url, Token: token, Language: lang.English, Version: "v0.0.1", SignalInterval: 50 * time.Millisecond, Logger: b.logger}
}

func (b *bench) waitFor(t *testing.T, name string, want func(machine.Status) bool) machine.Status {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		statuses, _ := b.machines.List(context.Background())
		for _, status := range statuses {
			if status.Name == name && want(status) {
				return status
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("machine %s never reached the wanted state", name)
	return machine.Status{}
}

func TestRun_EnrollsConnectsSignalsAndComesBackWithItsIdentity(t *testing.T) {
	b := newBench(t)
	token, _, err := b.machines.CreateToken(context.Background(), "vps-paris-1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, b.options(token)) }()

	online := b.waitFor(t, "vps-paris-1", func(s machine.Status) bool { return s.Online })
	if online.Address == "" || online.AgentVersion != "v0.0.1" {
		t.Fatalf("connected machine %+v", online)
	}
	b.waitFor(t, "vps-paris-1", func(s machine.Status) bool { return s.LastSeenAt.After(online.LastSeenAt) })
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
	b.waitFor(t, "vps-paris-1", func(s machine.Status) bool { return !s.Online })

	identity, err := LoadIdentity(b.stateDir)
	if err != nil || identity.MachineID != online.ID || identity.Server != b.url || identity.Language != lang.English {
		t.Fatalf("identity %+v %v", identity, err)
	}
	// Second démarrage : plus de jeton, l'identité suffit.
	ctx, cancel = context.WithCancel(context.Background())
	go func() {
		done <- Run(ctx, Options{StateDir: b.stateDir, Version: "v0.0.1", SignalInterval: 50 * time.Millisecond, Logger: b.logger})
	}()
	b.waitFor(t, "vps-paris-1", func(s machine.Status) bool { return s.Online })
	cancel()
	<-done
}

func TestRun_WithoutIdentityNorToken_Refuses(t *testing.T) {
	b := newBench(t)
	err := Run(context.Background(), Options{StateDir: b.stateDir, Logger: b.logger})
	if !errors.Is(err, ErrNotEnrolled) {
		t.Fatalf("got %v", err)
	}
}

func TestRun_BadToken_FailsClearly(t *testing.T) {
	b := newBench(t)
	err := Run(context.Background(), b.options("oc_nope"))
	var refused *ServerError
	if !errors.As(err, &refused) || refused.Code != "token_not_found" {
		t.Fatalf("got %v", err)
	}
	if _, err := LoadIdentity(b.stateDir); !errors.Is(err, ErrNotEnrolled) {
		t.Fatal("identity written despite the refusal")
	}
}

func TestRun_RemovedMachine_StopsForGood(t *testing.T) {
	b := newBench(t)
	token, _, _ := b.machines.CreateToken(context.Background(), "vps-lyon-2")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, b.options(token)) }()
	online := b.waitFor(t, "vps-lyon-2", func(s machine.Status) bool { return s.Online })
	if err := b.machines.Remove(context.Background(), online.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrIdentityRefused) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("agent kept retrying after removal")
	}
}

func TestReadEvent_ParsesEventsAndSkipsComments(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader(": ping\n\nevent: session\ndata: abc\n\nevent: closed\ndata: bye\n\n"))
	name, data, err := readEvent(reader)
	if err != nil || name != "session" || data != "abc" {
		t.Fatalf("%q %q %v", name, data, err)
	}
	name, data, err = readEvent(reader)
	if err != nil || name != "closed" || data != "bye" {
		t.Fatalf("%q %q %v", name, data, err)
	}
	if _, _, err := readEvent(reader); err == nil {
		t.Fatal("no EOF at the end")
	}
}

func TestPinnedTLS_RejectsAMalformedPin(t *testing.T) {
	if _, err := NewClient("https://example", "sha256:zz", "v"); err == nil {
		t.Fatal("bad pin accepted")
	}
	if _, err := NewClient("example.com", "", "v"); err == nil {
		t.Fatal("url without scheme accepted")
	}
}
