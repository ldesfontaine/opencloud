package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/firewall/opnsense/opnsensetest"
	"github.com/ldesfontaine/opencloud/internal/server/servertest"
)

// e2eAddrEnv porte l'adresse d'écoute du serveur du parcours navigateur
// (web/e2e). Sans elle, le test passe son tour : seul `make e2e` le lance.
const e2eAddrEnv = "OPENCLOUD_E2E_ADDR"

// TestE2EFixtureServer sert le vrai serveur sur des données fictives, celles
// des bancs de test, jusqu'au signal d'arrêt. Le parcours navigateur
// (Playwright) s'y branche : même base, mêmes composants, horloge figée, rien
// de réel. Les identifiants de l'opérateur sont posés sur chaque requête.
func TestE2EFixtureServer(t *testing.T) {
	addr := os.Getenv(e2eAddrEnv)
	if addr == "" {
		t.Skip(e2eAddrEnv + " n'est pas posée : serveur du parcours navigateur non demandé")
	}
	servertest.RequireFront(t)
	server := newTestServer(t)
	seedE2E(t, server)

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := &http.Server{Handler: server.handler}
	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Error(err)
		}
	}()
	t.Logf("serveur de parcours prêt sur %s", listener.Addr())

	stop, release := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer release()
	<-stop.Done()
	_ = httpServer.Close()
}

// seedE2E remplit chaque écran : une machine distante vue à l'instant, ses
// conteneurs et ses mesures, l'annuaire, la page de statut, les tâches, les
// sondes, les alertes, les ordres et le journal.
func seedE2E(t *testing.T, ts *testServer) {
	t.Helper()
	ts.seedAll(t)
	ts.seedResources(t)
	ts.seedProposals(t)
	ts.seedAlerts(t)
	ts.seedActions(t)
	ts.seedAudit(t)
	ts.signaledAt(t, remoteID, testNow)
	seedE2EBackups(t, ts)
	seedE2EFirewall(t, ts)
	seedE2EApply(t, ts)
}

// seedE2EBackups pose la clé, une destination sur la machine distante et le
// plan de la base : l'écran Sauvegardes et l'assistant de plan en ont besoin.
func seedE2EBackups(t *testing.T, ts *testServer) {
	t.Helper()
	for _, name := range []string{brand.SecretKeyFile, brand.ServerKeyFile} {
		if err := ts.state.WriteFile(name, []byte("une clé de test"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	remote, err := ts.machines.Get(context.Background(), remoteID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := ts.machines.Connect(context.Background(), remote.Machine, "192.0.2.1", "v0.0.1", "backup.receive=/srv/backups,backup.produce=proxmox-vzdump", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := callAPI(ts.Server, http.MethodPost, "/api/backups/keyring", map[string]any{"passphrase": backupPassphrase}); got.Code != http.StatusCreated {
		t.Fatalf("keyring: %d %s", got.Code, got.Body.String())
	}
	created := callAPI(ts.Server, http.MethodPost, "/api/backups/destinations", map[string]any{"name": "store-1", "machine_id": remoteID, "path": "/srv/backups"})
	if created.Code != http.StatusCreated {
		t.Fatalf("destination: %d %s", created.Code, created.Body.String())
	}
	var destination backupDestinationJSON
	decodeAPI(t, created, &destination)
	if got := callAPI(ts.Server, http.MethodPut, "/api/backups/plans/database", map[string]any{"source_kind": "database", "destination_id": destination.ID, "keep": 14}); got.Code != http.StatusOK {
		t.Fatalf("plan: %d %s", got.Code, got.Body.String())
	}
	// L'agent a rapporté ses droits puis s'est tu : sans flux ouvert, la machine
	// est hors ligne, comme sur une installation dont l'agent est arrêté.
	ts.machines.Disconnect(session)
}

// seedE2EFirewall déclare un pare-feu OPNsense sur le faux serveur des bancs.
func seedE2EFirewall(t *testing.T, ts *testServer) {
	t.Helper()
	declareFirewallThroughAPI(t, ts, opnsensetest.New(t))
}

// seedE2EApply passe un fichier modifié à l'annuaire : le journal des fichiers
// appliqués a une ligne, donc une page de détail.
func seedE2EApply(t *testing.T, ts *testServer) {
	t.Helper()
	exported := ts.exportedFile(t)
	file := strings.Replace(exported, "title: Nextcloud", "title: Nextcloud édité", 1)
	if file == exported {
		t.Fatal("le fichier exporté ne porte pas le titre à modifier")
	}
	var plan planJSON
	decodeAPI(t, callAPI(ts.Server, http.MethodPost, "/api/manifest/plan", manifestPayload(file, false, "")), &plan)
	if got := callAPI(ts.Server, http.MethodPost, "/api/manifest/apply", manifestPayload(file, false, plan.PlanHash)); got.Code != http.StatusCreated {
		t.Fatalf("apply: %d %s", got.Code, got.Body.String())
	}
}
