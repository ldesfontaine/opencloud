package server

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/restic"
)

// reportServiceDocker fait rapporter à la source les conteneurs du projet
// nextcloud (l'application et sa base postgres), un conteneur seul « pg »,
// et les volumes de la machine : deux montés par nextcloud, un par pg, un
// par un conteneur d'ailleurs.
func (b *resticBench) reportServiceDocker(t *testing.T) {
	t.Helper()
	observed := func(name, group, service, image, digit string) container.Observed {
		return container.Observed{
			ContainerID: strings.Repeat(digit, 64), Name: name, Group: group, Image: image, ImageID: "sha256:" + strings.Repeat(digit, 64),
			ComposeService: service, State: container.StateRunning, CreatedAt: testNow.Add(-time.Hour),
		}
	}
	report := container.Report{
		Complete: true,
		Inventory: []container.Observed{
			observed("nextcloud-app-1", "nextcloud", "app", "nextcloud:29.0.4", "a"),
			observed("nextcloud-db-1", "nextcloud", "db", "postgres:16.4", "b"),
			observed("pg", "", "", "postgres:16", "c"),
		},
		VolumesComplete: true,
		Volumes: []container.VolumeReport{
			{Name: "nextcloud_html", Driver: "local", UsedBy: []string{"nextcloud-app-1"}},
			{Name: "nextcloud_db", Driver: "local", UsedBy: []string{"nextcloud-db-1"}},
			{Name: "pg_data", Driver: "local", UsedBy: []string{"pg"}},
			{Name: "gitea_data", Driver: "local", UsedBy: []string{"gitea"}},
		},
	}
	if err := b.containers.Record(context.Background(), resticSourceID, report); err != nil {
		t.Fatal(err)
	}
}

// reconnectSource rouvre le flux de la source avec d'autres droits.
func (b *resticBench) reconnectSource(t *testing.T, rights string) {
	t.Helper()
	b.machines.Disconnect(b.source)
	status, err := b.machines.Get(context.Background(), resticSourceID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := b.machines.Connect(context.Background(), status.Machine, "192.0.2.21", "v0.0.1", rights, "", "")
	if err != nil {
		t.Fatal(err)
	}
	b.source = session
}

func (b *resticBench) serviceSource(t *testing.T, overview backupOverviewJSON, serviceID string) overviewSourceJSON {
	t.Helper()
	for _, source := range overview.Sources {
		if source.Key == "service:"+serviceID {
			return source
		}
	}
	t.Fatalf("no source for service %s: %+v", serviceID, overview.Sources)
	return overviewSourceJSON{}
}

// Le service entier : son dossier, les volumes que ses conteneurs montent et
// sa base, relus dans l'inventaire. Sans restic.docker, la création se
// refuse en le nommant ; avec, l'ordre porte les volumes et la base, et
// l'aperçu rend ce que chaque partie a donné.
func TestRestic_AServiceTakesItsVolumesAndItsDatabases(t *testing.T) {
	bench := readyResticBench(t)
	bench.reportServiceDocker(t)

	overview := bench.overview(t)
	want := []overviewSourcePartJSON{
		{Kind: "folder"}, {Kind: "volume", Name: "nextcloud_db"}, {Kind: "volume", Name: "nextcloud_html"},
		{Kind: "database", Name: "nextcloud-db-1", Engine: "postgres"},
	}
	if parts := bench.serviceSource(t, overview, bench.serviceID).Parts; !slices.Equal(parts, want) {
		t.Fatalf("parts %+v, want %+v", parts, want)
	}
	if !machineHasRight(overview, resticSourceID, machine.CapabilityResticDocker) {
		t.Fatalf("restic.docker is not in the rights of the source: %+v", overview.Machines)
	}
	got := callAPI(bench.Server, http.MethodPost, "/api/machines/"+resticSourceID+"/restic-backups", map[string]any{
		"name": "nextcloud", "scope": "service", "service_id": bench.serviceID, "target_machine_id": resticTargetID,
	})
	if got.Code != http.StatusConflict || errorCode(t, got) != "restic.source_docker_right_missing" {
		t.Fatalf("without restic.docker: %d %s", got.Code, got.Body.String())
	}

	bench.reconnectSource(t, "observe,restic.produce,restic.docker")
	backupID := bench.createServiceBackup(t)
	order := bench.runNow(t, backupID)
	var params agentwire.ResticBackupParams
	if err := json.Unmarshal(order.Params, &params); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(params.Volumes, []string{"nextcloud_db", "nextcloud_html"}) || !slices.Equal(params.Databases, []string{"nextcloud-db-1"}) {
		t.Fatalf("params %s", order.Params)
	}
	parts := []agentwire.ResticPartReport{
		{Kind: agentwire.ResticPartFolder, State: agentwire.ResticPartOK, BytesAdded: 10},
		{Kind: agentwire.ResticPartVolume, Name: "nextcloud_db", State: agentwire.ResticPartOK, BytesAdded: 20},
		{Kind: agentwire.ResticPartVolume, Name: "nextcloud_html", State: agentwire.ResticPartSkipped, Reason: agentwire.ReasonVolumeUnmounted},
		{Kind: agentwire.ResticPartDatabase, Name: "nextcloud-db-1", State: agentwire.ResticPartOK, BytesAdded: 30},
	}
	bench.reportRun(t, agentwire.ActionResult{RunID: order.RunID, State: agentwire.ActionSucceeded, Restic: &agentwire.ResticRunReport{
		Path: agentwire.ResticPathDirect, Reason: agentwire.ResticReachable, SnapshotID: strings.Repeat("a", 64), BytesAdded: 60, Parts: parts,
	}})

	protection := bench.resticProtection(t, bench.overview(t))
	wantResults := []overviewPartResultJSON{
		{Kind: "folder", State: "ok", BytesAdded: 10}, {Kind: "volume", Name: "nextcloud_db", State: "ok", BytesAdded: 20},
		{Kind: "volume", Name: "nextcloud_html", State: "skipped", Reason: "volume_unmounted"},
		{Kind: "database", Name: "nextcloud-db-1", State: "ok", BytesAdded: 30},
	}
	if !slices.Equal(protection.Parts, wantResults) || len(protection.Runs) != 1 || !slices.Equal(protection.Runs[0].Parts, wantResults) {
		t.Fatalf("parts %+v runs %+v", protection.Parts, protection.Runs)
	}
	if protection.LastSuccessAt == nil || protection.State != restic.ProtectionOK {
		t.Fatalf("protection %+v", protection)
	}
}

// Un échec garde la dernière réussite : l'écran dit « dernière réussite »
// à côté de l'échec. Sans nouvelle réussite au-delà de 36 h, la sauvegarde
// est en retard.
func TestRestic_AFailureKeepsTheLastSuccessAndAnOldSuccessIsLate(t *testing.T) {
	bench := readyResticBench(t)
	backupID := bench.createServiceBackup(t)
	order := bench.runNow(t, backupID)
	bench.reportRun(t, agentwire.ActionResult{RunID: order.RunID, State: agentwire.ActionSucceeded, Restic: &agentwire.ResticRunReport{
		Path: agentwire.ResticPathDirect, Reason: agentwire.ResticReachable, SnapshotID: strings.Repeat("a", 64), BytesAdded: 1,
	}})
	succeeded := bench.resticProtection(t, bench.overview(t))
	if succeeded.LastSuccessAt == nil {
		t.Fatalf("no last success: %+v", succeeded)
	}
	bench.restic.WaitBackground()

	failing := bench.runNow(t, backupID)
	bench.reportRun(t, agentwire.ActionResult{RunID: failing.RunID, State: agentwire.ActionFailed, Reason: agentwire.ReasonResticFailed,
		Restic: &agentwire.ResticRunReport{Path: agentwire.ResticPathDirect, Reason: agentwire.ResticReachable, Parts: []agentwire.ResticPartReport{
			{Kind: agentwire.ResticPartFolder, State: agentwire.ResticPartFailed, Reason: agentwire.ReasonResticFailed},
		}}})
	failed := bench.resticProtection(t, bench.overview(t))
	if failed.State != restic.ProtectionFailed || failed.LastSuccessAt == nil || !failed.LastSuccessAt.Equal(*succeeded.LastSuccessAt) ||
		len(failed.Parts) != 1 || failed.Parts[0].State != "failed" {
		t.Fatalf("after a failure: %+v", failed)
	}

	// Plus tard, une nouvelle réussite, puis 37 heures sans rien : en retard.
	bench.restic.WaitBackground()
	again := bench.runNow(t, backupID)
	bench.reportRun(t, agentwire.ActionResult{RunID: again.RunID, State: agentwire.ActionSucceeded, Restic: &agentwire.ResticRunReport{
		Path: agentwire.ResticPathDirect, Reason: agentwire.ResticReachable, SnapshotID: strings.Repeat("b", 64), BytesAdded: 1,
	}})
	bench.restic.SetClock(func() time.Time { return testNow.Add(37 * time.Hour) })
	if late := bench.resticProtection(t, bench.overview(t)); late.State != restic.ProtectionLate || late.LastSuccessAt == nil {
		t.Fatalf("37 hours later: %+v", late)
	}
}

// Un service sans dossier Compose (un conteneur seul) se sauvegarde par sa
// base ; un service qui n'a rien (un binaire) se refuse.
func TestRestic_AServiceWithoutFolderOrWithNothing(t *testing.T) {
	bench := readyResticBench(t)
	bench.reconnectSource(t, "observe,restic.produce,restic.docker")
	ctx := context.Background()
	alone, err := bench.directory.CreateService(ctx, directory.ServiceDefinition{
		MachineID: resticSourceID, Name: "pg", Nature: directory.NatureApplication, Origin: directory.OriginContainer,
		Units: []directory.UnitDefinition{{Name: "pg", Image: "postgres", Version: "16", ContainerName: "pg"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	binary, err := bench.directory.CreateService(ctx, directory.ServiceDefinition{
		MachineID: resticSourceID, Name: "caddy", Nature: directory.NatureInfra, Origin: directory.OriginBinary,
		Units: []directory.UnitDefinition{{Name: "caddy", Version: "2.8.4", SystemdUnit: "caddy.service"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	bench.reportServiceDocker(t)

	overview := bench.overview(t)
	want := []overviewSourcePartJSON{{Kind: "volume", Name: "pg_data"}, {Kind: "database", Name: "pg", Engine: "postgres"}}
	if parts := bench.serviceSource(t, overview, alone.ID).Parts; !slices.Equal(parts, want) {
		t.Fatalf("parts %+v", parts)
	}
	for _, source := range overview.Sources {
		if source.Key == "service:"+binary.ID {
			t.Fatalf("a service with nothing to save is not a source: %+v", source)
		}
	}
	got := callAPI(bench.Server, http.MethodPost, "/api/machines/"+resticSourceID+"/restic-backups", map[string]any{
		"name": "caddy", "scope": "service", "service_id": binary.ID, "target_machine_id": resticTargetID,
	})
	if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != "restic.service_nothing_to_save" {
		t.Fatalf("nothing to save: %d %s", got.Code, got.Body.String())
	}
	got = callAPI(bench.Server, http.MethodPost, "/api/machines/"+resticSourceID+"/restic-backups", map[string]any{
		"name": "pg", "scope": "service", "service_id": alone.ID, "target_machine_id": resticTargetID,
	})
	if got.Code != http.StatusCreated {
		t.Fatalf("a database alone: %d %s", got.Code, got.Body.String())
	}
}

func machineHasRight(overview backupOverviewJSON, machineID, right string) bool {
	for _, fit := range overview.Machines {
		if fit.MachineID == machineID {
			return slices.ContainsFunc(fit.Rights, func(candidate overviewRightJSON) bool { return candidate.Right == right })
		}
	}
	return false
}
