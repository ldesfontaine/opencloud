package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/restic"
)

// nightPass lance un passage de la nuit et rend le canal fermé à sa fin.
func nightPass(bench *resticBench) chan struct{} {
	passed := make(chan struct{})
	go func() {
		bench.restic.RunIfDue(context.Background())
		close(passed)
	}()
	return passed
}

func waitPass(t *testing.T, passed chan struct{}) {
	t.Helper()
	select {
	case <-passed:
	case <-time.After(30 * time.Second):
		t.Fatal("the night pass did not end")
	}
}

// succeeded : le rapport d'une sauvegarde réussie en direct.
func succeeded(runID, snapshot string) agentwire.ActionResult {
	return agentwire.ActionResult{RunID: runID, State: agentwire.ActionSucceeded, Restic: &agentwire.ResticRunReport{
		Path: agentwire.ResticPathDirect, Reason: agentwire.ResticReachable, SnapshotID: strings.Repeat(snapshot, 64), BytesAdded: 1,
	}}
}

// lastForget rend la dernière rétention que l'aperçu montre sur la
// sauvegarde du service.
func (b *resticBench) lastForget(t *testing.T) *lastForgetJSON {
	t.Helper()
	retention := b.resticProtection(t, b.overview(t)).Retention
	if !retention.Applied {
		t.Fatal("a restic retention is applied by the target")
	}
	return retention.LastForget
}

// Deux sources vers la même cible : la rétention attend la fin de toutes les
// sauvegardes de la nuit, puis part un dépôt après l'autre, jamais deux à la
// fois, et le dépôt purgé refuse une sauvegarde pendant ce temps. L'aperçu
// garde le résultat.
func TestRestic_RetentionsRunOneAtATimeAfterTheNight(t *testing.T) {
	bench := readyResticBench(t)
	bench.createServiceBackup(t)
	other := e2eConnect(t, bench.testServer, "docker1", dockerVMID, "observe,restic.produce")
	otherService := e2eComposeService(t, bench.testServer, dockerVMID, "paperless")
	got := callAPI(bench.Server, http.MethodPost, "/api/machines/"+dockerVMID+"/restic-backups", map[string]any{
		"name": "paperless", "scope": "service", "service_id": otherService, "target_machine_id": resticTargetID,
		"retention": map[string]int{"keep_daily": 30},
	})
	if got.Code != http.StatusCreated {
		t.Fatalf("second source: %d %s", got.Code, got.Body.String())
	}
	var otherBackup resticBackupCreatedJSON
	decodeAPI(t, got, &otherBackup)
	nextOrder(t, other)
	nextOrder(t, bench.target)
	source, second, target := tapOrders(t, bench.source), tapOrders(t, other), tapOrders(t, bench.target)

	passed := nightPass(bench)
	first := source.next(t, agentwire.KindResticBackup)
	late := second.next(t, agentwire.KindResticBackup)
	bench.reportRun(t, succeeded(first.RunID, "a"))
	target.none(t, "a backup of the night still runs")
	report := agentwire.ActionReport{Results: []agentwire.ActionResult{succeeded(late.RunID, "b")}}
	if got := signalAnnouncingSections(bench.testServer, other.Token, agentwire.SignalRequest{Actions: &report}); got.Code != http.StatusNoContent {
		t.Fatalf("second report: %d %s", got.Code, got.Body.String())
	}

	backupOf := map[string]string{resticSourceID: "", dockerVMID: otherBackup.ID}
	kept := map[string]agentwire.ResticRetainParams{}
	for range 2 {
		retain := target.next(t, agentwire.KindResticRetain)
		var params agentwire.ResticRetainParams
		if json.Unmarshal(retain.Params, &params) != nil {
			t.Fatal(retain.Params)
		}
		kept[params.User] = params
		target.none(t, "a retention is already running")
		if params.User == dockerVMID {
			busy := callAPI(bench.Server, http.MethodPost, "/api/machines/"+dockerVMID+"/restic-backups/"+backupOf[dockerVMID]+"/actions/run", map[string]any{})
			if busy.Code != http.StatusConflict || errorCode(t, busy) != "restic.backup_running" {
				t.Fatalf("a backup during the retention: %d %s", busy.Code, busy.Body.String())
			}
		}
		bench.reportTargetRun(t, agentwire.ActionResult{RunID: retain.RunID, State: agentwire.ActionSucceeded})
	}
	waitPass(t, passed)
	if kept[resticSourceID].KeepDaily != 14 || kept[resticSourceID].KeepWeekly != 8 || kept[resticSourceID].KeepMonthly != 3 || kept[dockerVMID].KeepDaily != 30 {
		t.Fatalf("retentions %+v", kept)
	}
	if forget := bench.lastForget(t); forget == nil || forget.State != restic.StateSucceeded || forget.At == nil {
		t.Fatalf("last forget %+v", forget)
	}
}

// Le plancher : une rétention qui garderait moins que celui que la cible a
// rapporté est refusée à la saisie (création, modification), un plancher ne
// s'abaisse pas par l'API, et la nuit n'envoie pas une politique que la cible
// refuserait. La cible reste juge : son refus est gardé tel quel.
func TestRestic_ARetentionBelowTheFloorIsRefused(t *testing.T) {
	bench := readyResticBench(t)
	low := map[string]any{
		"name": "nextcloud", "scope": "service", "service_id": bench.serviceID, "target_machine_id": resticTargetID,
		"retention": map[string]int{"keep_daily": 3, "keep_monthly": 12},
	}
	got := callAPI(bench.Server, http.MethodPost, "/api/machines/"+resticSourceID+"/restic-backups", low)
	if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != "restic.retention_below_floor" {
		t.Fatalf("create below the floor: %d %s", got.Code, got.Body.String())
	}
	low["retention"] = map[string]int{}
	if got := callAPI(bench.Server, http.MethodPost, "/api/machines/"+resticSourceID+"/restic-backups", low); errorCode(t, got) != "restic.retention_invalid" {
		t.Fatalf("an empty retention: %d %s", got.Code, got.Body.String())
	}
	backupID := bench.createServiceBackup(t)
	change := map[string]any{"name": "nextcloud", "retention": map[string]int{"keep_daily": 2}}
	got = callAPI(bench.Server, http.MethodPut, "/api/machines/"+resticSourceID+"/restic-backups/"+backupID, change)
	if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != "restic.retention_below_floor" {
		t.Fatalf("update below the floor: %d %s", got.Code, got.Body.String())
	}

	// Relever le plancher passe dans l'ordre ; l'abaisser est refusé.
	raise := map[string]any{"host": resticTargetHost, "port": resticTargetPort, "retention_floor": map[string]int{"keep_daily": 20}}
	if got := callAPI(bench.Server, http.MethodPost, "/api/machines/"+resticTargetID+"/actions/restic-target-setup", raise); got.Code != http.StatusNoContent {
		t.Fatalf("raise the floor: %d %s", got.Code, got.Body.String())
	}
	setup := nextOrder(t, bench.target)
	var params agentwire.ResticTargetSetupParams
	if json.Unmarshal(setup.Params, &params) != nil || params.RetentionFloor == nil || params.RetentionFloor.KeepDaily != 20 {
		t.Fatalf("setup %s", setup.Params)
	}
	stored, err := bench.db.GetResticTarget(context.Background(), resticTargetID)
	if err != nil {
		t.Fatal(err)
	}
	raised := runningTarget(stored.Reported.Certificate, stored.Reported.Fingerprint, resticSourceID)
	raised.RetentionFloor = &agentwire.ResticRetentionFloor{KeepDaily: 20}
	bench.reportTarget(t, raised)
	lower := map[string]any{"host": resticTargetHost, "port": resticTargetPort, "retention_floor": map[string]int{"keep_daily": 7}}
	if got := callAPI(bench.Server, http.MethodPost, "/api/machines/"+resticTargetID+"/actions/restic-target-setup", lower); got.Code != http.StatusConflict ||
		errorCode(t, got) != "restic.floor_lowered" {
		t.Fatalf("lower the floor: %d %s", got.Code, got.Body.String())
	}

	// La nuit : 14 nuits sous un plancher de 20, rien ne part à la cible.
	target := tapOrders(t, bench.target)
	passed := nightPass(bench)
	backup := nextOrder(t, bench.source)
	bench.reportRun(t, succeeded(backup.RunID, "c"))
	waitPass(t, passed)
	target.none(t, "a retention below the floor")
	if forget := bench.lastForget(t); forget == nil || forget.State != restic.StateRefused || forget.Reason != agentwire.ReasonRetentionBelowFloor {
		t.Fatalf("last forget %+v", forget)
	}

	// Le plancher rapporté redescend (fichier effacé sur la cible), mais la
	// cible refuse quand même : son refus est le résultat.
	bench.reportTarget(t, runningTarget(stored.Reported.Certificate, stored.Reported.Fingerprint, resticSourceID))
	bench.restic.SetClock(func() time.Time { return testNow.Add(24 * time.Hour) })
	passed = nightPass(bench)
	backup = nextOrder(t, bench.source)
	bench.reportRun(t, succeeded(backup.RunID, "d"))
	retain := target.next(t, agentwire.KindResticRetain)
	bench.reportTargetRun(t, agentwire.ActionResult{RunID: retain.RunID, State: agentwire.ActionRefused, Reason: agentwire.ReasonRetentionBelowFloor})
	waitPass(t, passed)
	if forget := bench.lastForget(t); forget == nil || forget.State != restic.StateRefused || forget.Reason != agentwire.ReasonRetentionBelowFloor {
		t.Fatalf("the refusal of the target: %+v", forget)
	}
}
