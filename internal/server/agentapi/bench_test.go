package agentapi

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/resource"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
	"github.com/ldesfontaine/opencloud/internal/server/servertest"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
	"github.com/ldesfontaine/opencloud/internal/update"
)

var testNow = servertest.Now()

const remoteID = servertest.RemoteID

// Le banc de l'agent : les vrais composants qu'il appelle, sur une vraie
// base SQLite temporaire, et ses routes montées sur un mux comme la racine
// les monte, sans la chaîne de middlewares (server la teste de bout en bout).
type bench struct {
	http.Handler
	handler    *Handler
	logger     *slog.Logger
	machines   *machine.Service
	resources  *resource.Service
	containers *container.Tracker
	probes     *probe.Service
	updates    *update.Service
	actions    *action.Service
	db         *store.DB
}

func newBench(t *testing.T) *bench {
	t.Helper()
	db := storetest.Open(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	clock := func() time.Time { return testNow }
	machines := machine.New(db, machine.NewSessions(), logger)
	machines.SetClock(clock)
	storetest.EnsureLocalMachine(t, machines)
	resources := resource.New(db, logger)
	resources.SetClock(clock)
	containers := container.New(db, logger)
	containers.SetClock(clock)
	probes := probe.New(db, logger)
	probes.SetClock(clock)
	updates := update.New(db, logger)
	updates.SetClock(clock)
	actions := action.New(db, machines, logger)
	actions.SetClock(clock)
	actions.SetKey(action.GenerateKey())
	b := &bench{logger: logger, machines: machines, resources: resources, containers: containers, probes: probes, updates: updates, actions: actions, db: db}
	b.mount()
	return b
}

// mount refait le handler avec les composants du banc et monte ses routes.
func (b *bench) mount() {
	b.handler = New(Options{
		Logger:        b.logger,
		Machines:      b.machines,
		Resources:     b.resources,
		Containers:    b.containers,
		Probes:        b.probes,
		Updates:       b.updates,
		Actions:       b.actions,
		ClientAddress: httpx.PeerHost,
		Clock:         func() time.Time { return testNow },
	})
	mux := http.NewServeMux()
	b.handler.Routes(func(method, pattern string, handler http.HandlerFunc) {
		mux.HandleFunc(method+" "+pattern, handler)
	})
	b.Handler = mux
}

func (b *bench) enroll(t *testing.T, name, id string) (machine.Machine, ed25519.PrivateKey) {
	t.Helper()
	return storetest.EnrollMachineWithKey(t, b.machines, id, name)
}

func (b *bench) requestChallenge(t *testing.T, machineID, source string) string {
	t.Helper()
	return servertest.RequestChallenge(t, b, machineID, source)
}

func (b *bench) openStream(t *testing.T, machineID string, private ed25519.PrivateKey) (*http.Response, string, *bufio.Reader, context.CancelFunc) {
	t.Helper()
	return servertest.OpenStream(t, b, machineID, private, b.requestChallenge(t, machineID, ""))
}

func (b *bench) postJSON(path string, payload any) *httptest.ResponseRecorder {
	body, _ := json.Marshal(payload)
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	b.ServeHTTP(recorder, request)
	return recorder
}
