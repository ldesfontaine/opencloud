package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/live"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/mcp"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/resource"
	"github.com/ldesfontaine/opencloud/internal/service"
	"github.com/ldesfontaine/opencloud/internal/settings"
	"github.com/ldesfontaine/opencloud/internal/status"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/update"
)

// L'horloge figée des tests : « vu il y a » se calcule depuis elle.
var testNow = time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)

type testServer struct {
	*Server
	machines   *machine.Service
	heartbeats *heartbeat.Service
	resources  *resource.Service
	services   *service.Tracker
	probes     *probe.Service
	updates    *update.Service
	status     *status.Service
	alerts     *alert.Engine
	notifier   *alert.Notifier
	access     *mcp.Service
	bus        *live.Bus
	publicBus  *live.Bus
	db         *store.DB
}

// Le serveur de test tourne sur le vrai composant machine et une vraie base
// SQLite temporaire, avec la machine openCloud déjà en place.
func newTestServer(t *testing.T) *testServer {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	db, err := store.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	bus := live.New()
	machines := machine.New(db, machine.NewSessions(), logger)
	machines.SetClock(func() time.Time { return testNow })
	machines.SetListener(bus)
	err = machines.EnsureLocal(context.Background(), machine.LocalInfo{
		Hostname: "opencloud-host", Address: "10.8.0.1", OS: "Debian 12", Arch: "amd64", Version: "v0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	heartbeats := heartbeat.New(db, logger)
	heartbeats.SetClock(func() time.Time { return testNow })
	heartbeats.SetWatcher(bus)
	resources := resource.New(db, logger)
	resources.SetClock(func() time.Time { return testNow })
	resources.SetWatcher(bus)
	services := service.New(db, logger)
	services.SetClock(func() time.Time { return testNow })
	services.SetWatcher(bus)
	probes := probe.New(db, logger)
	probes.SetClock(func() time.Time { return testNow })
	probes.SetWatcher(bus)
	updates := update.New(db, logger)
	updates.SetClock(func() time.Time { return testNow })
	updates.SetWatcher(bus)
	preferences := settings.New(root)
	publicBus := live.New()
	statusPage := status.New(db, machines, preferences, logger)
	statusPage.SetClock(func() time.Time { return testNow })
	statusPage.AddWatcher(bus)
	statusPage.AddWatcher(publicBus)
	catalogs, err := lang.Load()
	if err != nil {
		t.Fatal(err)
	}
	alerts := alert.New(db, logger)
	alerts.SetClock(func() time.Time { return testNow })
	alerts.SetWatcher(bus)
	alerts.SetMaintenance(statusPage)
	notifier := alert.NewNotifier(db, catalogs, func() lang.Code { return lang.French }, logger)
	notifier.SetClock(func() time.Time { return testNow })
	alerts.SetSender(notifier)
	machines.SetAlerter(alerts)
	heartbeats.SetListener(alerts)
	resources.SetAlerter(alerts)
	services.SetAlerter(alerts)
	probes.SetAlerter(alerts)
	access := mcp.New(db, logger)
	access.SetClock(func() time.Time { return testNow })
	server, err := New(Options{
		Logger:     logger,
		Version:    "v0.0.1",
		Settings:   preferences,
		Machines:   machines,
		Heartbeats: heartbeats,
		Resources:  resources,
		Services:   services,
		Probes:     probes,
		Updates:    updates,
		Status:     statusPage,
		Alerts:     alerts,
		Notifier:   notifier,
		MCP:        access,
		Live:       bus,
		PublicLive: publicBus,
		ConfigPath: "/etc/opencloud/config.toml",
		Clock:      func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	// Le serveur commande les agents par leurs flux, comme dans serve.
	services.SetCommander(server)
	probes.SetCommander(server)
	updates.SetCommander(server)
	return &testServer{Server: server, machines: machines, heartbeats: heartbeats, resources: resources, services: services, probes: probes, updates: updates, status: statusPage, alerts: alerts, notifier: notifier, access: access, bus: bus, publicBus: publicBus, db: db}
}

// Enrôle une machine distante avec un id fixe, pour des rendus figés.
func (ts *testServer) enroll(t *testing.T, name, id string) (machine.Machine, ed25519.PrivateKey) {
	t.Helper()
	cleartext, _, err := ts.machines.CreateToken(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	enrolled, err := ts.machines.Enroll(context.Background(), machine.Enrollment{
		MachineID: id, PublicKey: public, Token: cleartext,
		Hostname: name, Address: "51.15.20.114", OS: "Debian 12", Arch: "amd64", AgentVersion: "v0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return enrolled, private
}

// L'identifiant de la machine distante des tests, fixe pour des rendus figés.
const remoteID = "11111111-2222-4333-8444-555555555555"

// L'arbre des routes est figé dans testdata/routes.txt : le modifier est
// une décision, pas un effet de bord.
func TestRoutes_MatchFrozenTree(t *testing.T) {
	server := newTestServer(t)
	var lines []string
	for _, route := range server.routes() {
		lines = append(lines, route.method+" "+route.pattern)
	}
	got := strings.Join(lines, "\n") + "\n"
	want, err := os.ReadFile("testdata/routes.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("routes changed:\n%s\nwant:\n%s", got, want)
	}
}
