package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
)

const (
	restoreSourceRights = "observe,restic.produce,restic.restore,files.write=/srv/data"
	restoreSnapshot     = "4444444444444444444444444444444444444444444444444444444444444444"
	restoreStamp        = "20260912-140000"
)

func (b *resticBench) restoreBackup(backupID string, body map[string]any) *httptest.ResponseRecorder {
	return callAPI(b.Server, http.MethodPost, "/api/machines/"+resticSourceID+"/restic-backups/"+backupID+"/actions/restore", body)
}

func restoreBody(path string) map[string]any {
	return map[string]any{"snapshot": restoreSnapshot, "path": path, "stamp": restoreStamp}
}

// Sans le droit restic.restore sur la SOURCE, rien ne part, et le refus porte
// sa clé : l'écran dit le droit et propose « Ré-enrôler avec ce droit ».
func TestRestic_RestoreNeedsTheRightOnTheSource(t *testing.T) {
	bench := readyResticBench(t)
	backupID := bench.createServiceBackup(t)
	source := tapOrders(t, bench.source)

	got := bench.restoreBackup(backupID, restoreBody("/srv/data/app.ini"))
	if got.Code != http.StatusConflict || errorCode(t, got) != "restic.source_restore_right_missing" {
		t.Fatalf("restore without the right: %d %s", got.Code, got.Body.String())
	}
	source.none(t, "a refused restore")
}

// Hors de la forme (instantané, chemin avec « .. », racine, horodatage) : refusé
// avant tout ordre.
func TestRestic_RestoreRefusesAFormOutsideItsBounds(t *testing.T) {
	bench := readyResticBench(t)
	backupID := bench.createServiceBackup(t)
	bench.reconnectSource(t, restoreSourceRights)
	source := tapOrders(t, bench.source)
	for _, body := range []map[string]any{
		{"snapshot": "latest", "path": "/srv/data/a", "stamp": restoreStamp},
		{"snapshot": restoreSnapshot, "path": "/srv/data/../etc/passwd", "stamp": restoreStamp},
		{"snapshot": restoreSnapshot, "path": "/", "stamp": restoreStamp},
		{"snapshot": restoreSnapshot, "path": "srv/data/a", "stamp": restoreStamp},
		{"snapshot": restoreSnapshot, "path": "/srv/data/a", "stamp": "demain"},
		{"snapshot": restoreSnapshot, "path": "/srv/data/a", "stamp": "20260912-1400"},
		{"snapshot": restoreSnapshot, "path": "/srv/data/a"},
	} {
		got := bench.restoreBackup(backupID, body)
		if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != "restic.restore_request" {
			t.Fatalf("%v: %d %s", body, got.Code, got.Body.String())
		}
	}
	source.none(t, "a refused restore")
}

// L'ordre part à la source, sans secret : le mot de passe du dépôt, semé, n'est
// ni dans l'ordre, ni dans la réponse, ni dans l'audit, ni dans le journal, ni
// dans la base. Le résultat de la source se lit sur l'ordre, et l'audit garde
// l'instantané, le chemin et la destination exacte.
func TestRestic_RestoreSendsOneOrderWithoutSecretAndAuditsTheDestination(t *testing.T) {
	bench := readyResticBench(t)
	backupID := bench.createServiceBackup(t)
	bench.reconnectSource(t, restoreSourceRights)
	source := tapOrders(t, bench.source)

	got := bench.restoreBackup(backupID, restoreBody("/srv/data/app.ini"))
	if got.Code != http.StatusCreated {
		t.Fatalf("restore: %d %s", got.Code, got.Body.String())
	}
	order := source.next(t, agentwire.KindResticRestore)
	var params agentwire.ResticRestoreParams
	if json.Unmarshal(order.Params, &params) != nil || params.Snapshot != restoreSnapshot || params.Path != "/srv/data/app.ini" || params.Stamp != restoreStamp {
		t.Fatalf("params %s", order.Params)
	}
	if len(order.Secret) != 0 {
		t.Fatal("restic.restore carries no secret: the source reads its own password file")
	}

	places := map[string]string{
		"response":      got.Body.String(),
		"order":         string(order.Params),
		"journal":       bench.logs.String(),
		"database dump": dumpDatabase(t, bench),
		"overview":      callAPI(bench.Server, http.MethodGet, "/api/backups/overview", nil).Body.String(),
		"actions":       callAPI(bench.Server, http.MethodGet, "/api/actions", nil).Body.String(),
		"audit":         callAPI(bench.Server, http.MethodGet, "/api/audit?limit=500", nil).Body.String(),
	}
	for place, content := range places {
		for _, secret := range []string{bench.provisioned.RepoPassword, bench.provisioned.RESTPassword} {
			if strings.Contains(content, secret) {
				t.Errorf("a repository secret leaked into %s", place)
			}
		}
	}
	audit := places["audit"]
	for _, want := range []string{"restic_backup.restore", restoreSnapshot, "/srv/data/app.ini", "/srv/data/app.ini.restaure-" + restoreStamp} {
		if !strings.Contains(audit, want) {
			t.Errorf("the audit lacks %q", want)
		}
	}
}

// Les refus de la source (la destination existe, le chemin n'est pas dans
// l'instantané) se lisent sur l'ordre, avec leur raison fermée.
func TestRestic_RestoreShowsWhatTheSourceAnswered(t *testing.T) {
	bench := readyResticBench(t)
	backupID := bench.createServiceBackup(t)
	bench.reconnectSource(t, restoreSourceRights)
	source := tapOrders(t, bench.source)
	for _, reason := range []string{agentwire.ReasonPathExists, agentwire.ReasonPathNotInSnapshot} {
		if got := bench.restoreBackup(backupID, restoreBody("/srv/data/app.ini")); got.Code != http.StatusCreated {
			t.Fatalf("restore: %d %s", got.Code, got.Body.String())
		}
		order := source.next(t, agentwire.KindResticRestore)
		report := agentwire.ActionReport{Results: []agentwire.ActionResult{{RunID: order.RunID, State: agentwire.ActionRefused, Reason: reason}}}
		if r := signalAnnouncingSections(bench.testServer, bench.source.Token, agentwire.SignalRequest{Actions: &report}); r.Code != http.StatusNoContent {
			t.Fatalf("report: %d %s", r.Code, r.Body.String())
		}
		runs := callAPI(bench.Server, http.MethodGet, "/api/actions?machine="+resticSourceID, nil).Body.String()
		if !strings.Contains(runs, `"reason":"`+reason+`"`) {
			t.Fatalf("the run does not say %q:\n%s", reason, runs)
		}
	}
}

// Une destination hors des dossiers de files.write est refusée par le serveur
// avant l'ordre (l'agent la refuserait aussi).
func TestRestic_RestoreRefusesADestinationOutsideFilesWrite(t *testing.T) {
	bench := readyResticBench(t)
	backupID := bench.createServiceBackup(t)
	bench.reconnectSource(t, restoreSourceRights)
	source := tapOrders(t, bench.source)
	got := bench.restoreBackup(backupID, restoreBody("/etc/ssh/sshd_config"))
	if got.Code != http.StatusConflict || errorCode(t, got) != "restic.restore_outside_right" {
		t.Fatalf("outside files.write: %d %s", got.Code, got.Body.String())
	}
	source.none(t, "a refused restore")
}
