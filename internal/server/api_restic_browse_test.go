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
	browseRights   = "observe,restic.target=/srv/restic,restic.browse"
	browseSnapshot = "3333333333333333333333333333333333333333333333333333333333333333"
	// Un nom de fichier choisi pour être cherché partout : il peut passer par
	// l'écran qui le demande, nulle part ailleurs.
	browsedName = "finances-2025.ods"
)

func (b *resticBench) browseBackup(backupID string, body map[string]any) *httptest.ResponseRecorder {
	return callAPI(b.Server, http.MethodPost, "/api/machines/"+resticSourceID+"/restic-backups/"+backupID+"/actions/browse", body)
}

// browseAsync lance la requête, qui attend la cible, et rend de quoi la lire.
func (b *resticBench) browseAsync(backupID string, body map[string]any) func() *httptest.ResponseRecorder {
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- b.browseBackup(backupID, body) }()
	return func() *httptest.ResponseRecorder { return <-done }
}

func TestRestic_BrowseNeedsTheRightOnTheTarget(t *testing.T) {
	bench := readyResticBench(t)
	backupID := bench.createServiceBackup(t)
	target := tapOrders(t, bench.target)

	got := bench.browseBackup(backupID, map[string]any{})
	if got.Code != http.StatusConflict || errorCode(t, got) != "restic.target_browse_right_missing" {
		t.Fatalf("browse without the right: %d %s", got.Code, got.Body.String())
	}
	target.none(t, "a refused browse")
}

func TestRestic_BrowseRefusesAFormOutsideItsBounds(t *testing.T) {
	bench := readyResticBenchRights(t, browseRights)
	backupID := bench.createServiceBackup(t)
	for _, body := range []map[string]any{
		{"snapshot": "latest", "path": "/"}, {"snapshot": browseSnapshot, "path": "/a/../b"}, {"snapshot": browseSnapshot}, {"path": "/x"},
	} {
		got := bench.browseBackup(backupID, body)
		if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != "restic.browse_request" {
			t.Fatalf("%v: %d %s", body, got.Code, got.Body.String())
		}
	}
}

// Le parcours part à la cible avec le seul mot de passe du dépôt dans le
// secret de l'ordre ; les noms reviennent à l'écran qui les demande et ne sont
// gardés nulle part : ni base, ni journal, ni audit. Le mot de passe non plus.
func TestRestic_BrowseReturnsNamesToTheAskerAndKeepsNothing(t *testing.T) {
	bench := readyResticBenchRights(t, browseRights)
	backupID := bench.createServiceBackup(t)
	target := tapOrders(t, bench.target)

	wait := bench.browseAsync(backupID, map[string]any{"snapshot": browseSnapshot, "path": "/home"})
	order := target.next(t, agentwire.KindResticBrowse)
	var secret map[string]string
	if json.Unmarshal(order.Secret, &secret) != nil || len(secret) != 1 || secret["repo_password"] != bench.provisioned.RepoPassword {
		t.Fatal("restic.browse carries the repository password, and only it")
	}
	bench.reportTargetRun(t, agentwire.ActionResult{
		RunID: order.RunID, State: agentwire.ActionSucceeded,
		Browse: &agentwire.ResticBrowseReport{Entries: []agentwire.BrowseEntry{{Name: browsedName, Kind: "file", Size: 23, ModTime: "2026-10-01T00:00:00Z"}}},
	})
	got := wait()
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), browsedName) {
		t.Fatalf("the asker gets the names: %d %s", got.Code, got.Body.String())
	}

	places := map[string]string{
		"journal":       bench.logs.String(),
		"database dump": dumpDatabase(t, bench),
		"overview":      callAPI(bench.Server, http.MethodGet, "/api/backups/overview", nil).Body.String(),
		"actions":       callAPI(bench.Server, http.MethodGet, "/api/actions", nil).Body.String(),
		"audit":         callAPI(bench.Server, http.MethodGet, "/api/audit?limit=500", nil).Body.String(),
	}
	for place, content := range places {
		for _, leaked := range []string{bench.provisioned.RepoPassword, browsedName} {
			if strings.Contains(content, leaked) {
				t.Errorf("%q leaked into %s", leaked, place)
			}
		}
	}
	if !strings.Contains(places["audit"], "restic_backup.browse") {
		t.Error("the browse is not in the audit")
	}
}

// Un rapport hors bornes n'est jamais cru ; une cible qui refuse ou échoue
// donne une raison fermée.
func TestRestic_BrowseJudgesWhatTheTargetReports(t *testing.T) {
	bench := readyResticBenchRights(t, browseRights)
	backupID := bench.createServiceBackup(t)
	target := tapOrders(t, bench.target)
	cases := []struct {
		result agentwire.ActionResult
		code   string
	}{
		{agentwire.ActionResult{State: agentwire.ActionSucceeded, Browse: &agentwire.ResticBrowseReport{Entries: []agentwire.BrowseEntry{{Name: "a/b", Kind: "file"}}}}, "restic.browse_failed"},
		{agentwire.ActionResult{State: agentwire.ActionFailed, Reason: agentwire.ReasonRepoEmpty}, "restic.browse_repo_empty"},
		{agentwire.ActionResult{State: agentwire.ActionFailed, Reason: agentwire.ReasonResticFailed}, "restic.browse_failed"},
	}
	for _, c := range cases {
		wait := bench.browseAsync(backupID, map[string]any{})
		order := target.next(t, agentwire.KindResticBrowse)
		c.result.RunID = order.RunID
		bench.reportTargetRun(t, c.result)
		if got := wait(); got.Code != http.StatusConflict || errorCode(t, got) != c.code {
			t.Fatalf("%+v: %d %s", c.result, got.Code, got.Body.String())
		}
	}
}
