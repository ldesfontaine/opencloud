package server

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/backup"
	"github.com/ldesfontaine/opencloud/internal/goldentest"
)

// runsBench déclare un hôte Proxmox par l'API, comme l'opérateur : son
// relevé contient la VM 5012, dont l'archive est sur le stockage « local », et
// le conteneur 200, qui n'en a pas. Deux lignes d'historique y sont posées.
func runsBench(t *testing.T) *testServer {
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

func TestBackupRuns_FiltersAndRefusesABadLimit(t *testing.T) {
	server := runsBench(t)
	bearer := server.issueAPIToken(t, "mcp-maison")

	all := callBearer(server.Server, "/api/backups/runs", bearer)
	var runs backupRunsJSON
	decodeAPI(t, all, &runs)
	if all.Code != http.StatusOK || len(runs.Runs) != 2 || runs.Runs[0].Result != "failed" || runs.Runs[1].Result != "ok" {
		t.Fatalf("runs %d %s", all.Code, all.Body.String())
	}
	goldentest.Compare(t, filepath.Join("testdata", "backups_runs.golden.json"), normalizeBackups(all.Body.String()))

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
