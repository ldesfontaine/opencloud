package public

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/live"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
	"github.com/ldesfontaine/opencloud/internal/server/servertest"
	"github.com/ldesfontaine/opencloud/internal/settings"
	"github.com/ldesfontaine/opencloud/internal/status"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
)

var testNow = servertest.Now()

const testPage = `<!doctype html><title>statut</title><div id="root"></div>`

// Le banc de la page publique : le vrai composant statut sur une vraie base
// SQLite temporaire, son bus public, et les routes montées sur un mux comme
// la racine les monte, sans la chaîne de middlewares (server la teste de
// bout en bout).
type bench struct {
	http.Handler
	handler    *Handler
	status     *status.Service
	machines   *machine.Service
	heartbeats *heartbeat.Service
	probes     *probe.Service
	bus        *live.Bus
}

func newBench(t *testing.T) *bench {
	t.Helper()
	db := storetest.Open(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	clock := func() time.Time { return testNow }
	machines := machine.New(db, machine.NewSessions(), logger)
	machines.SetClock(clock)
	heartbeats := heartbeat.New(db, logger)
	heartbeats.SetClock(clock)
	probes := probe.New(db, logger)
	probes.SetClock(clock)
	preferences := settings.New(db, logger)
	statusPage := status.New(db, machines, preferences, logger)
	statusPage.SetClock(clock)
	bus := live.New()
	statusPage.AddWatcher(bus)
	catalogs, err := lang.Load()
	if err != nil {
		t.Fatal(err)
	}
	b := &bench{status: statusPage, machines: machines, heartbeats: heartbeats, probes: probes, bus: bus}
	b.handler = New(Options{
		Logger:        logger,
		Status:        statusPage,
		Catalogs:      catalogs,
		Live:          bus,
		Page:          func() ([]byte, error) { return []byte(testPage), nil },
		ClientAddress: httpx.PeerHost,
		Clock:         clock,
	})
	b.mount(b.handler)
	return b
}

// mount monte les routes du handler sur un mux neuf.
func (b *bench) mount(handler *Handler) {
	mux := http.NewServeMux()
	handler.Routes(func(method, pattern string, route http.HandlerFunc) {
		if method != anyMethod {
			pattern = method + " " + pattern
		}
		mux.HandleFunc(pattern, route)
	})
	b.Handler = mux
}

// get joue un GET depuis l'adresse de pair par défaut des requêtes de test.
func (b *bench) get(path string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	b.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder
}

// getFrom joue un GET depuis une adresse de pair donnée et rend son statut.
func (b *bench) getFrom(peer, path string) int {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.RemoteAddr = peer
	recorder := httptest.NewRecorder()
	b.ServeHTTP(recorder, request)
	return recorder.Code
}

// seed pose un composant « Nuage » fait d'une machine, d'une sonde et d'une
// tâche, un incident ouvert dessus et le titre de la page : de quoi vérifier
// que rien de ces objets ne sort en public.
func (b *bench) seed(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	storetest.EnrollMachine(t, b.machines, servertest.RemoteID, "vps-paris-1")
	job, err := b.heartbeats.Create(ctx, heartbeat.Definition{Name: "sauvegarde nextcloud", MachineID: servertest.RemoteID, Interval: 24 * time.Hour, Grace: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	site, err := b.probes.Create(ctx, probe.Definition{
		Name: "site nextcloud", Kind: probe.KindHTTP, Target: "https://cloud.exemple.fr/status.php", MachineID: servertest.RemoteID,
	})
	if err != nil {
		t.Fatal(err)
	}
	cloud, err := b.status.CreateComponent(ctx, status.ComponentDefinition{Name: "Nuage", Members: []status.MemberRef{
		{Kind: status.KindMachine, ID: servertest.RemoteID},
		{Kind: status.KindHeartbeat, ID: job.ID},
		{Kind: status.KindProbe, ID: site.ID},
	}})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := b.status.OpenIncident(ctx, status.IncidentDefinition{
		Title: "Le nuage ne répond plus", Impact: status.ImpactDown, Status: status.StatusInvestigating,
		Message: "Nous regardons.", ComponentIDs: []string{cloud.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.status.AddUpdate(ctx, opened.ID, status.StatusIdentified, "Le disque est plein."); err != nil {
		t.Fatal(err)
	}
	if _, err := b.status.SetPage(ctx, status.Page{Title: "Statut de la maison", Announcement: "Coupure électrique samedi, de 8 h à 10 h."}); err != nil {
		t.Fatal(err)
	}
}

// openLive ouvre le direct public depuis une adresse de pair donnée et rend
// un lecteur d'événements nommés. Le serveur d'essai pose RemoteAddr d'après
// un en-tête de test : le reste est le vrai chemin (limiteur, places par
// source, pool global).
func (b *bench) openLive(t *testing.T, peer, lastEventID string) (func() string, *http.Response, context.CancelFunc) {
	t.Helper()
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if peer := r.Header.Get("X-Test-Peer"); peer != "" {
			r.RemoteAddr = peer
		}
		b.ServeHTTP(w, r)
	})
	liveServer := httptest.NewServer(wrapped)
	t.Cleanup(liveServer.Close)
	ctx, cancel := context.WithCancel(context.Background())
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, liveServer.URL+"/statut/api/events", nil)
	request.Header.Set("X-Test-Peer", peer)
	if lastEventID != "" {
		request.Header.Set("Last-Event-ID", lastEventID)
	}
	response, err := liveServer.Client().Do(request)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = response.Body.Close() })
	reader := bufio.NewReader(response.Body)
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
	return next, response, cancel
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

var errNotBuilt = errors.New("statut.html is not there")
