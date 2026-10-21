package server

import (
	"context"
	"crypto/ed25519"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/auth"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/lifecycle"
	"github.com/ldesfontaine/opencloud/internal/live"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/mcp"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/resource"
	"github.com/ldesfontaine/opencloud/internal/server/servertest"
	"github.com/ldesfontaine/opencloud/internal/settings"
	"github.com/ldesfontaine/opencloud/internal/status"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
	"github.com/ldesfontaine/opencloud/internal/update"
)

// L'horloge figée des tests : « vu il y a » se calcule depuis elle.
var testNow = servertest.Now()

type testServer struct {
	*Server
	machines   *machine.Service
	heartbeats *heartbeat.Service
	resources  *resource.Service
	containers *container.Tracker
	probes     *probe.Service
	directory  *directory.Directory
	updates    *update.Service
	lifecycle  *lifecycle.Service
	support    *supportSource
	status     *status.Service
	alerts     *alert.Engine
	notifier   *alert.Notifier
	access     *mcp.Service
	operator   *auth.Service
	bus        *live.Bus
	publicBus  *live.Bus
	db         *store.DB
}

// Le serveur de test tourne sur le vrai composant machine et une vraie base
// SQLite temporaire, avec la machine openCloud déjà en place. Il est
// configuré comme une installation qui a passé sa première configuration : un
// compte basic, dont les identifiants sont posés sur toute requête de
// l'opérateur qui n'en porte pas, pour que les tests qui ne parlent pas de
// l'accès n'aient pas à les écrire. Ceux qui en parlent partent de
// newFirstSetupServer.
func newTestServer(t *testing.T) *testServer {
	t.Helper()
	server := newFirstSetupServer(t)
	server.enableBasic(t)
	server.handler = asOperator(server.handler)
	return server
}

// anonymousHeader marque la requête d'un test qui joue un inconnu : le banc
// n'y pose pas d'identifiants (voir callAs).
const anonymousHeader = "X-Test-Anonymous"

// asOperator pose les identifiants du compte de test sur une requête qui n'en
// porte aucun. Les routes ouvertes les ignorent, sauf /mcp et le point de
// jeton OAuth, où Authorization est la preuve d'un agent IA : une requête
// sans rien y reste sans rien.
func asOperator(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		anonymous := r.Header.Get(anonymousHeader) != ""
		r.Header.Del(anonymousHeader)
		if !anonymous && r.Header.Get("Authorization") == "" && r.URL.Path != mcpPath && r.URL.Path != mcpTokenPath {
			r.SetBasicAuth(testUsername, testPassword)
		}
		next.ServeHTTP(w, r)
	})
}

// newFirstSetupServer est une installation neuve : mode none, aucun compte,
// rien de posé sur les requêtes.
func newFirstSetupServer(t *testing.T) *testServer {
	t.Helper()
	root, db := storetest.OpenWithStateDir(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	bus := live.New()
	machines := machine.New(db, machine.NewSessions(), logger)
	machines.SetClock(func() time.Time { return testNow })
	machines.SetWatcher(bus)
	storetest.EnsureLocalMachine(t, machines)
	heartbeats := heartbeat.New(db, logger)
	heartbeats.SetClock(func() time.Time { return testNow })
	heartbeats.SetWatcher(bus)
	resources := resource.New(db, logger)
	resources.SetClock(func() time.Time { return testNow })
	resources.SetWatcher(bus)
	containers := container.New(db, logger)
	containers.SetClock(func() time.Time { return testNow })
	containers.SetWatcher(bus)
	probes := probe.New(db, logger)
	probes.SetClock(func() time.Time { return testNow })
	probes.SetWatcher(bus)
	declared := directory.New(db, logger)
	declared.SetClock(func() time.Time { return testNow })
	declared.SetWatcher(bus)
	updates := update.New(db, logger)
	updates.SetClock(func() time.Time { return testNow })
	updates.SetWatcher(bus)
	// Le même câblage que serve : l'annuaire lit les conteneurs et leurs
	// constats, le suivi des conteneurs lui demande leurs unités.
	declared.SetObserved(containers, updates)
	declared.SetRematcher(containers)
	containers.SetUnits(declared)
	// Les réponses figées datent d'avant la fin de support : le serveur de
	// test démarre avec le réglage coupé, et les tests qui le regardent
	// l'activent. Qu'il soit activé sur une installation neuve se vérifie
	// dans le paquet store.
	preferences := settings.New(db, logger)
	if _, err := preferences.SetLifecycleEnabled(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	support := newSupportSource()
	lifecycles := lifecycle.New(containers.Images, machines.Systems, lifecycle.NewFileCache(root), support, preferences.CurrentLifecycleEnabled, logger)
	lifecycles.SetClock(func() time.Time { return testNow })
	lifecycles.SetWatcher(bus)
	lifecycles.SetPause(0)
	publicBus := live.New()
	statusPage := status.New(db, db, machines, preferences, logger)
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
	heartbeats.SetAlerter(alerts)
	resources.SetAlerter(alerts)
	containers.SetAlerter(alerts)
	probes.SetAlerter(alerts)
	access := mcp.New(db, logger)
	access.SetClock(func() time.Time { return testNow })
	operator, err := auth.New(db, logger)
	if err != nil {
		t.Fatal(err)
	}
	operator.SetClock(func() time.Time { return testNow })
	// Le coût minimal de bcrypt : le coût réel ferait durer chaque test.
	operator.SetHashCost(bcrypt.MinCost)
	server, err := New(Options{
		Logger:     logger,
		Version:    "v0.0.1",
		Reader:     db,
		Settings:   preferences,
		Machines:   machines,
		Heartbeats: heartbeats,
		Resources:  resources,
		Containers: containers,
		Probes:     probes,
		Directory:  declared,
		Manifest:   newTestManifest(declared, probes, heartbeats, db, bus, logger),
		Updates:    updates,
		Lifecycle:  lifecycles,
		Status:     statusPage,
		Alerts:     alerts,
		Notifier:   notifier,
		MCP:        access,
		Auth:       operator,
		Live:       bus,
		PublicLive: publicBus,
		ConfigPath: "/etc/opencloud/config.toml",
		// httptest donne Host: example.com à ses requêtes : c'est l'hôte
		// d'écoute de ce serveur, donc un Host reconnu.
		Listen: "example.com:80",
		Clock:  func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	// Le serveur commande les agents par leurs flux, comme dans serve.
	containers.SetCommander(server)
	probes.SetCommander(server)
	updates.SetCommander(server)
	return &testServer{Server: server, machines: machines, heartbeats: heartbeats, resources: resources, containers: containers, probes: probes, directory: declared, updates: updates, lifecycle: lifecycles, support: support, status: statusPage, alerts: alerts, notifier: notifier, access: access, operator: operator, bus: bus, publicBus: publicBus, db: db}
}

// Enrôle une machine distante avec un id fixe, pour des rendus figés.
func (ts *testServer) enroll(t *testing.T, name, id string) (machine.Machine, ed25519.PrivateKey) {
	t.Helper()
	return storetest.EnrollMachineWithKey(t, ts.machines, id, name)
}

// L'identifiant de la machine distante des tests, fixe pour des rendus figés.
const remoteID = servertest.RemoteID

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
