package server

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/backup"
)

// inventoryBench déclare un hôte Proxmox par l'API, comme l'opérateur : son
// relevé contient la VM 5012, dont l'archive est sur le stockage « local », et
// le conteneur 200, qui n'en a pas. Deux lignes d'historique y sont posées.
func inventoryBench(t *testing.T) *testServer {
	t.Helper()
	server, api, _ := proxmoxBench(t)
	if got := putProxmox(server, pveID, api.request(pveTokenSecret)); got.Code != http.StatusOK {
		t.Fatalf("declare: %d %s", got.Code, got.Body.String())
	}
	size := int64(4096)
	at := time.Unix(1_800_000_000, 0)
	for _, record := range []backup.RunRecord{
		{Subject: "pve", PassAt: at, Source: backup.GuestSourceKey(pveID, 5012), StartedAt: at, FinishedAt: at.Add(time.Minute),
			Result: backup.ResultOK, SizeBytes: &size, DestinationID: "bkd_1", DestinationName: "store-1", ActionRunID: "run_1"},
		{Subject: "pve", PassAt: at, Source: backup.GuestSourceKey(pveID, 200), StartedAt: at, FinishedAt: at.Add(time.Minute),
			Result: backup.ResultFailed, Reason: "producer_failed", DestinationID: "bkd_1", DestinationName: "store-1"},
	} {
		if err := server.db.RecordBackupRun(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}
	return server
}

// Le fichier vzdump que le relevé a vu est rattaché à la VM de son numéro, et
// l'inventaire le rend à l'opérateur et au jeton de lecture, identique.
func TestBackupInventory_ReadsTheObservedArchiveOfTheRightSource(t *testing.T) {
	server := inventoryBench(t)
	bearer := server.issueAPIToken(t, "mcp-maison")

	byOperator := callAPI(server.Server, http.MethodGet, "/api/backups/inventory", nil)
	byToken := callBearer(server.Server, "/api/backups/inventory", bearer)
	if byOperator.Code != http.StatusOK || byToken.Code != http.StatusOK {
		t.Fatalf("operator %d %s, token %d %s", byOperator.Code, byOperator.Body.String(), byToken.Code, byToken.Body.String())
	}
	if byOperator.Body.String() != byToken.Body.String() {
		t.Fatalf("the token reads another inventory:\n%s\n%s", byOperator.Body.String(), byToken.Body.String())
	}

	var inventory backupInventoryJSON
	decodeAPI(t, byToken, &inventory)
	sources := map[string]backupSourceJSON{}
	for _, source := range inventory.Sources {
		sources[source.Key] = source
	}
	vm := sources[backup.GuestSourceKey(pveID, 5012)]
	if vm.Kind != "vm" || vm.Observed == nil || vm.Observed.Storage != "local" || vm.Observed.Count != 1 ||
		vm.Observed.TotalBytes != 244318208 || vm.LastOK == nil || vm.LastOK.SizeBytes == nil || *vm.LastOK.SizeBytes != 4096 {
		t.Fatalf("vm 5012 %+v", vm)
	}
	lxc := sources[backup.GuestSourceKey(pveID, 200)]
	if lxc.Kind != "lxc" || lxc.Observed != nil || lxc.LastOK != nil || lxc.LastRun == nil || lxc.LastRun.Reason != "producer_failed" {
		t.Fatalf("lxc 200 %+v", lxc)
	}
	if _, found := sources[backup.SourceDatabaseKey]; !found {
		t.Fatalf("the database is a source: %+v", inventory.Sources)
	}
	if len(inventory.Targets) != 1 || inventory.Targets[0].Kind != "storage" || inventory.Targets[0].Name != "local" {
		t.Fatalf("targets %+v", inventory.Targets)
	}
	compareGolden(t, filepath.Join("testdata", "backups_inventory.golden.json"), normalizeBackups(byToken.Body.String()))
}

func TestBackupRuns_FiltersAndRefusesABadLimit(t *testing.T) {
	server := inventoryBench(t)
	bearer := server.issueAPIToken(t, "mcp-maison")

	all := callBearer(server.Server, "/api/backups/runs", bearer)
	var runs backupRunsJSON
	decodeAPI(t, all, &runs)
	if all.Code != http.StatusOK || len(runs.Runs) != 2 || runs.Runs[0].Result != "failed" || runs.Runs[1].Result != "ok" {
		t.Fatalf("runs %d %s", all.Code, all.Body.String())
	}
	compareGolden(t, filepath.Join("testdata", "backups_runs.golden.json"), normalizeBackups(all.Body.String()))

	filtered := callBearer(server.Server, "/api/backups/runs?plan=pve&source="+backup.GuestSourceKey(pveID, 5012)+"&limit=1", bearer)
	decodeAPI(t, filtered, &runs)
	if filtered.Code != http.StatusOK || len(runs.Runs) != 1 || runs.Runs[0].Result != "ok" {
		t.Fatalf("filtered %d %s", filtered.Code, filtered.Body.String())
	}
	none := callBearer(server.Server, "/api/backups/runs?plan=database", bearer)
	decodeAPI(t, none, &runs)
	if none.Code != http.StatusOK || runs.Runs == nil || len(runs.Runs) != 0 {
		t.Fatalf("no run %d %s", none.Code, none.Body.String())
	}
	for _, limit := range []string{"0", "-3", "abc"} {
		if got := callBearer(server.Server, "/api/backups/runs?limit="+limit, bearer); got.Code != http.StatusBadRequest {
			t.Errorf("limit %s: %d", limit, got.Code)
		}
	}
}

// L'inventaire et l'historique se lisent, rien de plus : aucune route de ces
// chemins n'est autre chose qu'un GET, et ni le jeton ni l'opérateur n'y
// écrivent.
func TestBackupInventory_NoRouteAcceptsAWrite(t *testing.T) {
	server := newTestServer(t)
	bearer := server.issueAPIToken(t, "mcp-maison")
	seen := 0
	for _, route := range server.routes() {
		if !strings.HasPrefix(route.pattern, "/api/backups/inventory") && !strings.HasPrefix(route.pattern, "/api/backups/runs") {
			continue
		}
		seen++
		if route.method != http.MethodGet {
			t.Errorf("%s %s: an inventory route that is not a read", route.method, route.pattern)
		}
	}
	if seen != 2 {
		t.Fatalf("%d inventory routes, want 2", seen)
	}
	for _, path := range []string{"/api/backups/inventory", "/api/backups/runs"} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			if got := callBearerWith(server.Server, method, path, bearer, ""); got.Code != http.StatusForbidden {
				t.Errorf("token %s %s: %d, want 403", method, path, got.Code)
			}
			if got := callAPI(server.Server, method, path, map[string]any{}); got.Code < 400 {
				t.Errorf("operator %s %s: %d, a write was accepted", method, path, got.Code)
			}
		}
	}
	if got := callBearerWith(server.Server, http.MethodGet, "/api/backups/inventory", "ock_"+strings.Repeat("0", 40), ""); got.Code != http.StatusUnauthorized {
		t.Errorf("an unknown token reads the inventory: %d", got.Code)
	}
	if got := callBearerWith(server.Server, http.MethodGet, "/api/backups", bearer, ""); got.Code != http.StatusForbidden {
		t.Errorf("the page of the backups stays closed to a token: %d", got.Code)
	}
}
