package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/restic"
)

const verifyRights = "observe,restic.target=/srv/restic,restic.verify"

func (b *resticBench) verifyBackup(backupID string) *httptest.ResponseRecorder {
	return callAPI(b.Server, http.MethodPost, "/api/machines/"+resticSourceID+"/restic-backups/"+backupID+"/actions/verify", map[string]any{})
}

// Sans le droit restic.verify, la cible ne reçoit rien : le refus porte sa
// clé, que l'écran traduit en « Ré-enrôler avec ce droit ».
func TestRestic_VerifyNeedsTheRightOnTheTarget(t *testing.T) {
	bench := readyResticBench(t)
	backupID := bench.createServiceBackup(t)
	target := tapOrders(t, bench.target)

	got := bench.verifyBackup(backupID)
	if got.Code != http.StatusConflict || errorCode(t, got) != "restic.target_verify_right_missing" {
		t.Fatalf("verify without the right: %d %s", got.Code, got.Body.String())
	}
	target.none(t, "a refused verification")
	if verify := bench.resticProtection(t, bench.overview(t)).Verify; verify == nil || verify.Right != "missing" || verify.State != "" {
		t.Fatalf("the overview must say the right is missing: %+v", verify)
	}
}

// La vérification part à la cible avec le seul mot de passe du dépôt dans le
// secret de l'ordre ; ses comptes reviennent par son signal, l'aperçu les
// garde, et le mot de passe n'est ni dans la base en clair, ni dans l'API,
// ni dans le journal.
func TestRestic_VerifyRunsOnTheTargetAndKeepsOnlyCounts(t *testing.T) {
	bench := readyResticBenchRights(t, verifyRights)
	backupID := bench.createServiceBackup(t)
	target := tapOrders(t, bench.target)

	got := bench.verifyBackup(backupID)
	if got.Code != http.StatusCreated {
		t.Fatalf("verify: %d %s", got.Code, got.Body.String())
	}
	order := target.next(t, agentwire.KindResticVerify)
	var params agentwire.ResticVerifyParams
	var secret map[string]string
	if json.Unmarshal(order.Params, &params) != nil || params.User != resticSourceID {
		t.Fatalf("params %s", order.Params)
	}
	if json.Unmarshal(order.Secret, &secret) != nil || len(secret) != 1 || secret["repo_password"] != bench.provisioned.RepoPassword {
		t.Fatal("restic.verify carries the repository password, and only it")
	}
	if running := bench.resticProtection(t, bench.overview(t)).Verify; running == nil || running.State != restic.StateSent {
		t.Fatalf("a verification in flight: %+v", running)
	}

	bench.reportTargetRun(t, agentwire.ActionResult{
		RunID: order.RunID, State: agentwire.ActionSucceeded, Verify: &agentwire.ResticVerifyReport{Snapshots: 2, Files: 4, Bytes: 4096},
	})
	verify := bench.resticProtection(t, bench.overview(t)).Verify
	if verify == nil || verify.State != restic.StateSucceeded || verify.Snapshots != 2 || verify.Files != 4 || verify.Bytes != 4096 ||
		verify.SuccessAt == nil || verify.Right != "ok" || verify.Stale {
		t.Fatalf("verification kept: %+v", verify)
	}

	places := map[string]string{
		"journal":       bench.logs.String(),
		"database dump": dumpDatabase(t, bench),
		"overview":      callAPI(bench.Server, http.MethodGet, "/api/backups/overview", nil).Body.String(),
		"actions":       callAPI(bench.Server, http.MethodGet, "/api/actions", nil).Body.String(),
		"audit":         callAPI(bench.Server, http.MethodGet, "/api/audit?limit=500", nil).Body.String(),
	}
	for place, content := range places {
		if strings.Contains(content, bench.provisioned.RepoPassword) {
			t.Errorf("the repository password leaked into %s", place)
		}
	}
	if !strings.Contains(places["audit"], "restic_backup.verify") {
		t.Error("the verification is not in the audit")
	}
}

// Un échec garde sa raison fermée et la dernière réussite ; des comptes hors
// bornes ne sont jamais crus.
func TestRestic_AFailedVerificationKeepsTheLastSuccess(t *testing.T) {
	bench := readyResticBenchRights(t, verifyRights)
	backupID := bench.createServiceBackup(t)
	target := tapOrders(t, bench.target)

	run := func(result agentwire.ActionResult) {
		t.Helper()
		if got := bench.verifyBackup(backupID); got.Code != http.StatusCreated {
			t.Fatalf("verify: %d %s", got.Code, got.Body.String())
		}
		order := target.next(t, agentwire.KindResticVerify)
		result.RunID = order.RunID
		bench.reportTargetRun(t, result)
	}
	run(agentwire.ActionResult{State: agentwire.ActionSucceeded, Verify: &agentwire.ResticVerifyReport{Snapshots: 1, Files: 2, Bytes: 10}})
	good := bench.resticProtection(t, bench.overview(t)).Verify
	if good == nil || good.SuccessAt == nil {
		t.Fatalf("first verification: %+v", good)
	}

	run(agentwire.ActionResult{State: agentwire.ActionFailed, Reason: agentwire.ReasonCheckFailed})
	failed := bench.resticProtection(t, bench.overview(t)).Verify
	if failed.State != restic.StateFailed || failed.Reason != agentwire.ReasonCheckFailed || failed.SuccessAt == nil || !failed.SuccessAt.Equal(*good.SuccessAt) {
		t.Fatalf("failed verification: %+v", failed)
	}

	run(agentwire.ActionResult{State: agentwire.ActionSucceeded, Verify: &agentwire.ResticVerifyReport{Snapshots: 9999, Files: 1}})
	absurd := bench.resticProtection(t, bench.overview(t)).Verify
	if absurd.State != restic.StateFailed || absurd.Files != 0 {
		t.Fatalf("out-of-bounds counts were believed: %+v", absurd)
	}
	if _, err := bench.db.FindResticLinkByVerifyRun(context.Background(), resticTargetID, "inconnu"); err == nil {
		t.Fatal("an unknown run must not match a link")
	}
}
