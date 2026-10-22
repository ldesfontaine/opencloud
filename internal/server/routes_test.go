package server

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/apitoken"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/auth"
	"github.com/ldesfontaine/opencloud/internal/backup"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/lifecycle"
	"github.com/ldesfontaine/opencloud/internal/live"
	"github.com/ldesfontaine/opencloud/internal/machine"
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
	apiTokens  *apitoken.Service
	operator   *auth.Service
	journal    *audit.Recorder
	actions    *action.Service
	backups    *backup.Service
	state      *os.Root
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
// porte aucun. Les routes ouvertes les ignorent ; une requête qui porte déjà
// Authorization (un Bearer d'API, par exemple) garde la sienne.
func asOperator(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		anonymous := r.Header.Get(anonymousHeader) != ""
		r.Header.Del(anonymousHeader)
		if !anonymous && r.Header.Get("Authorization") == "" {
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
	statusPage := status.New(db, db, machines, logger)
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
	notifier := alert.NewNotifier(db, catalogs, func() lang.Code { return lang.French }, logger)
	notifier.SetClock(func() time.Time { return testNow })
	alerts.SetSender(notifier)
	machines.SetAlerter(alerts)
	heartbeats.SetAlerter(alerts)
	resources.SetAlerter(alerts)
	containers.SetAlerter(alerts)
	probes.SetAlerter(alerts)
	apiTokens := apitoken.New(db, logger)
	apiTokens.SetClock(func() time.Time { return testNow })
	operator, err := auth.New(db, logger)
	if err != nil {
		t.Fatal(err)
	}
	operator.SetClock(func() time.Time { return testNow })
	// Le coût minimal de bcrypt : le coût réel ferait durer chaque test.
	operator.SetHashCost(bcrypt.MinCost)
	// Le journal d'audit, branché comme dans serve.
	journal := audit.New(db, logger)
	journal.SetClock(func() time.Time { return testNow })
	preferences.SetAuditor(journal)
	machines.SetAuditor(journal)
	heartbeats.SetAuditor(journal)
	probes.SetAuditor(journal)
	declared.SetAuditor(journal)
	statusPage.SetAuditor(journal)
	alerts.SetAuditor(journal)
	apiTokens.SetAuditor(journal)
	operator.SetAuditor(journal)
	// Les ordres, signés par une clé figée : les rendus le sont aussi.
	actions := action.New(db, machines, logger)
	actions.SetClock(func() time.Time { return testNow })
	actions.SetWatcher(bus)
	actions.SetAuditor(journal)
	actions.SetKey(testServerKey(t))
	// La sauvegarde : une vraie boîte à colis sous l'état de test, une phrase
	// scellée à bas coût.
	outbox, err := action.NewOutbox(root)
	if err != nil {
		t.Fatal(err)
	}
	actions.SetOutbox(outbox)
	backups := backup.New(db, machines, root, logger)
	backups.SetClock(func() time.Time { return testNow })
	backups.SetWorkFactor(10)
	backups.SetSender(actions)
	backups.SetParcels(outbox)
	backups.SetWatcher(bus)
	backups.SetAuditor(journal)
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
		APITokens:  apiTokens,
		Auth:       operator,
		Actions:    actions,
		Backups:    backups,
		Live:       bus,
		PublicLive: publicBus,
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
	actions.SetCommander(server)
	return &testServer{actions: actions, backups: backups, state: root, Server: server, machines: machines, heartbeats: heartbeats, resources: resources, containers: containers, probes: probes, directory: declared, updates: updates, lifecycle: lifecycles, support: support, status: statusPage, alerts: alerts, notifier: notifier, apiTokens: apiTokens, operator: operator, journal: journal, bus: bus, publicBus: publicBus, db: db}
}

// testServerKey lit une clé du serveur figée, posée dans un fichier comme
// serve la pose : son empreinte est la même d'un passage à l'autre.
func testServerKey(t *testing.T) *action.Key {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	seed := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("k", ed25519.SeedSize)))
	if err := root.WriteFile(brand.ServerKeyFile, []byte("ed25519-v1:"+seed+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := action.LoadKey(root)
	if err != nil {
		t.Fatal(err)
	}
	return key
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
