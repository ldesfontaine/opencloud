package server

import (
	"context"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/brand"
)

// La phrase de test : des mots, pas un secret.
const backupPassphrase = "une phrase de test assez longue" // gitleaks:allow

var (
	backupRecipient   = regexp.MustCompile(`age1[a-z0-9]{40,}`)
	backupFingerprint = regexp.MustCompile(`"fingerprint":"[0-9a-f]{16}"`)
	backupKeyFile     = regexp.MustCompile(`opencloud-key-[0-9a-f]{16}\.age`)
	backupDestination = regexp.MustCompile(`bkd_[0-9a-f]{16}`)
	backupSum         = regexp.MustCompile(`"sha256":"[0-9a-f]{64}"`)
	backupSize        = regexp.MustCompile(`"size":[0-9]+`)
	backupParcel      = regexp.MustCompile(`"parcel":"[0-9a-f]+"`)
)

// normalizeBackups fige ce qui est tiré à chaque sauvegarde : la clé, les
// identifiants, les sommes et les tailles de l'archive chiffrée.
func normalizeBackups(body string) string {
	body = normalize(body)
	body = backupRecipient.ReplaceAllString(body, "age1_RECIPIENT")
	body = backupFingerprint.ReplaceAllString(body, `"fingerprint":"FINGERPRINT"`)
	body = backupKeyFile.ReplaceAllString(body, "opencloud-key-FINGERPRINT.age")
	body = backupDestination.ReplaceAllString(body, "bkd_ID")
	body = backupSum.ReplaceAllString(body, `"sha256":"SUM"`)
	body = backupSize.ReplaceAllString(body, `"size":0`)
	return backupParcel.ReplaceAllString(body, `"parcel":"ID"`)
}

// backupSetup crée la clé, la destination et le plan par l'API, comme la
// page le fait, sur une machine dont l'agent a rapporté son droit.
func backupSetup(t *testing.T, server *testServer) (destinationID string) {
	t.Helper()
	// Les deux clés que l'archive emporte avec la base.
	for _, name := range []string{brand.SecretKeyFile, brand.ServerKeyFile} {
		if err := server.state.WriteFile(name, []byte("une clé de test"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	enrolled, _ := server.enroll(t, "vps-paris-1", remoteID)
	if _, err := server.machines.Connect(context.Background(), enrolled, "192.0.2.1", "v0.0.1", "backup.receive=/srv/backups", "", ""); err != nil {
		t.Fatal(err)
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/backups/keyring", map[string]any{"passphrase": backupPassphrase}); got.Code != http.StatusCreated {
		t.Fatalf("keyring: %d %s", got.Code, got.Body.String())
	}
	created := callAPI(server.Server, http.MethodPost, "/api/backups/destinations", map[string]any{"name": "store-1", "machine_id": remoteID, "path": "/srv/backups"})
	if created.Code != http.StatusCreated {
		t.Fatalf("destination: %d %s", created.Code, created.Body.String())
	}
	var destination backupDestinationJSON
	decodeAPI(t, created, &destination)
	if destination.Right != "ok" || destination.MachineName != "vps-paris-1" {
		t.Fatalf("destination %+v", destination)
	}
	if got := callAPI(server.Server, http.MethodPut, "/api/backups/plan", map[string]any{"destination_id": destination.ID, "keep": 14}); got.Code != http.StatusOK {
		t.Fatalf("plan: %d %s", got.Code, got.Body.String())
	}
	return destination.ID
}

// Rien n'est réglé : la page voit une clé absente, aucune destination, aucun
// plan. Une fois réglé, et un ordre parti, elle voit tout, jamais la phrase
// ni la part privée de la clé.
func TestBackups_ReadsMatchGoldenFiles(t *testing.T) {
	server := newTestServer(t)
	empty := callAPI(server.Server, http.MethodGet, "/api/backups", nil)
	if empty.Code != http.StatusOK {
		t.Fatalf("empty: %d %s", empty.Code, empty.Body.String())
	}
	compareGolden(t, filepath.Join("testdata", "backups_empty.golden.json"), normalizeBackups(empty.Body.String()))

	backupSetup(t, server)
	run := callAPI(server.Server, http.MethodPost, "/api/backups/actions/run", map[string]any{})
	if run.Code != http.StatusCreated {
		t.Fatalf("run: %d %s", run.Code, run.Body.String())
	}
	full := callAPI(server.Server, http.MethodGet, "/api/backups", nil)
	if full.Code != http.StatusOK {
		t.Fatalf("full: %d %s", full.Code, full.Body.String())
	}
	for _, secret := range []string{backupPassphrase, "AGE-SECRET-KEY", "identity"} {
		if strings.Contains(full.Body.String(), secret) {
			t.Fatalf("the page reads %q: %s", secret, full.Body.String())
		}
	}
	compareGolden(t, filepath.Join("testdata", "backups.golden.json"), normalizeBackups(full.Body.String()))
}

// Chaque refus du composant arrive par sa clé, avec le statut de sa nature :
// 422 pour une saisie, 409 pour l'état présent, 404 pour un objet absent.
func TestBackups_MapsEachRefusalToItsKey(t *testing.T) {
	server := newTestServer(t)
	call := func(method, path string, payload any, status int, key string) {
		t.Helper()
		got := callAPI(server.Server, method, path, payload)
		if got.Code != status || (key != "" && errorCode(t, got) != key) {
			t.Fatalf("%s %s: %d %s, want %d %s", method, path, got.Code, got.Body.String(), status, key)
		}
	}
	call(http.MethodPost, "/api/backups/keyring", map[string]any{"passphrase": "trop courte"}, http.StatusUnprocessableEntity, "backup.passphrase_too_short")
	call(http.MethodPost, "/api/backups/actions/run", map[string]any{}, http.StatusConflict, "backup.plan_missing")
	call(http.MethodDelete, "/api/backups/destinations/bkd_inconnue", nil, http.StatusNotFound, "not_found")
	call(http.MethodDelete, "/api/backups/plan", nil, http.StatusNotFound, "not_found")
	call(http.MethodPost, "/api/backups/destinations", map[string]any{"name": "Store 1", "machine_id": remoteID, "path": "/srv/backups"}, http.StatusUnprocessableEntity, "backup.name_invalid")
	call(http.MethodPost, "/api/backups/destinations", map[string]any{"name": "store-1", "machine_id": remoteID, "path": "/srv/backups"}, http.StatusUnprocessableEntity, "backup.machine_unknown")
	call(http.MethodPost, "/api/backups/keyring", map[string]any{"passphrase": backupPassphrase, "unknown": true}, http.StatusBadRequest, "bad_json")

	destinationID := backupSetup(t, server)
	call(http.MethodPost, "/api/backups/keyring", map[string]any{"passphrase": backupPassphrase}, http.StatusConflict, "backup.keyring_exists")
	call(http.MethodPost, "/api/backups/keyring", map[string]any{"passphrase": backupPassphrase, "replace": true}, http.StatusCreated, "")
	call(http.MethodPost, "/api/backups/destinations", map[string]any{"name": "store-2", "machine_id": remoteID, "path": "/srv/ailleurs"}, http.StatusConflict, "backup.right_missing")
	call(http.MethodDelete, "/api/backups/destinations/"+destinationID, nil, http.StatusConflict, "backup.destination_in_use")
	call(http.MethodPut, "/api/backups/plan", map[string]any{"destination_id": destinationID, "keep": 999}, http.StatusUnprocessableEntity, "backup.keep_invalid")

	// Le plan arrêté, la destination se retire ; la machine qui la porte
	// n'est plus retenue.
	call(http.MethodDelete, "/api/backups/plan", nil, http.StatusNoContent, "")
	call(http.MethodDelete, "/api/backups/destinations/"+destinationID, nil, http.StatusNoContent, "")
}

// La sauvegarde envoie deux ordres signés, la réception puis l'élagage, et
// l'audit garde les gestes sans la phrase.
func TestBackups_RunSendsReceiveThenPruneAndAuditsWithoutThePassphrase(t *testing.T) {
	server := newTestServer(t)
	backupSetup(t, server)
	run := callAPI(server.Server, http.MethodPost, "/api/backups/actions/run", map[string]any{})
	if run.Code != http.StatusCreated {
		t.Fatalf("run: %d %s", run.Code, run.Body.String())
	}
	var sent actionRunJSON
	decodeAPI(t, run, &sent)
	if sent.Kind != string(agentwire.KindBackupReceive) || sent.State != "sent" {
		t.Fatalf("run %+v", sent)
	}
	runs, err := server.db.ListActionRuns(context.Background(), action.Query{MachineID: remoteID, Limit: 10})
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs %+v %v", runs, err)
	}
	kinds := []agentwire.Kind{runs[0].Kind, runs[1].Kind}
	if !slices.Contains(kinds, agentwire.KindBackupReceive) || !slices.Contains(kinds, agentwire.KindBackupPrune) {
		t.Fatalf("kinds %v", kinds)
	}
	events, err := server.db.ListAuditEvents(context.Background(), audit.Query{Limit: 100})
	if err != nil || len(events) == 0 {
		t.Fatalf("audit %v %v", events, err)
	}
	var actions []audit.Action
	for _, event := range events {
		actions = append(actions, event.Action)
		for key, value := range event.Details {
			if strings.Contains(key, "passphrase") || strings.Contains(value, backupPassphrase) {
				t.Fatalf("the audit keeps the passphrase: %+v", event)
			}
		}
	}
	for _, want := range []audit.Action{audit.BackupKeyringCreate, audit.BackupDestinationCreate, audit.BackupPlanUpdate, audit.BackupPlanRun} {
		found := false
		for _, got := range actions {
			found = found || got == want
		}
		if !found {
			t.Errorf("no %s in %v", want, actions)
		}
	}
}
