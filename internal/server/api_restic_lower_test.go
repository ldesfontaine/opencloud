package server

import (
	"net/http"
	"strings"
	"testing"
)

// Baisser une rétention purge plus : l'API exige le nom actuel de la
// sauvegarde et écrit une ligne d'audit avec les comptes d'avant et d'après.
// Une hausse (ou un simple renommage) ne demande rien.
func TestRestic_LoweringARetentionAsksForTheNameAndIsAudited(t *testing.T) {
	bench := readyResticBench(t)
	backupID := bench.createServiceBackup(t)
	url := "/api/machines/" + resticSourceID + "/restic-backups/" + backupID
	lower := func(confirm string) map[string]any {
		return map[string]any{"name": "nextcloud", "confirm_name": confirm, "retention": map[string]int{"keep_daily": 10, "keep_weekly": 8, "keep_monthly": 3}}
	}

	for _, wrong := range []string{"", "Nextcloud", "nextcloud ", "autre"} {
		got := callAPI(bench.Server, http.MethodPut, url, lower(wrong))
		if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != "restic.retention_confirm_name" {
			t.Fatalf("lowering with %q: %d %s", wrong, got.Code, got.Body.String())
		}
	}
	if strings.Contains(callAPI(bench.Server, http.MethodGet, "/api/audit?limit=500", nil).Body.String(), "retention_lower") {
		t.Fatal("a refused lowering is audited as done")
	}

	if got := callAPI(bench.Server, http.MethodPut, url, lower("nextcloud")); got.Code != http.StatusNoContent {
		t.Fatalf("lowering with the name: %d %s", got.Code, got.Body.String())
	}
	audit := callAPI(bench.Server, http.MethodGet, "/api/audit?limit=500", nil).Body.String()
	for _, want := range []string{"restic_backup.retention_lower", "daily=14", "daily=10"} {
		if !strings.Contains(audit, want) {
			t.Errorf("the audit lacks %q: %s", want, audit)
		}
	}

	raise := map[string]any{"name": "nextcloud 2", "retention": map[string]int{"keep_daily": 30, "keep_weekly": 8, "keep_monthly": 3}}
	if got := callAPI(bench.Server, http.MethodPut, url, raise); got.Code != http.StatusNoContent {
		t.Fatalf("raising and renaming asks for nothing: %d %s", got.Code, got.Body.String())
	}
}
