package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"testing"

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
}
