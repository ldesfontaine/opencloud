package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/firewall/opnsense/opnsensetest"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// Le plan d'export d'un pare-feu passe par la même route que les autres plans :
// aucun verbe nouveau. Banc de non-fuite : ni la paire de clés du pare-feu, ni
// le contenu de sa configuration, n'apparaissent dans une réponse, l'audit, le
// journal, ni un ordre.
func TestBackups_FirewallExportPlanNeverLeaksTheKeysOrTheConfiguration(t *testing.T) {
	server, fake, journal := firewallBench(t)
	destinationID := backupSetup(t, server)
	// Le droit de destination doit couvrir l'élagage par préfixe.
	remote, err := server.machines.Get(context.Background(), remoteID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.machines.Connect(context.Background(), remote.Machine, "192.0.2.1", machine.PrunePrefixSinceVersion, "backup.receive=/srv/backups", "", ""); err != nil {
		t.Fatal(err)
	}
	firewallID, _ := declareFirewallThroughAPI(t, server, fake)
	body := map[string]any{"name": "Pare-feu de bordure", "source_kind": "agent", "producer": "opnsense-config", "selector": firewallID, "destination_id": destinationID, "keep": 7}

	// Sans le droit « Diagnostics: Configuration History », le plan n'est pas créé.
	fake.Answer(opnsensetest.ExportPath, http.StatusForbidden, `{"status":403}`)
	refused := callAPI(server.Server, http.MethodPut, "/api/backups/plans/bordure", body)
	if refused.Code != http.StatusUnprocessableEntity || errorCode(t, refused) != "firewall.export_forbidden" {
		t.Fatalf("forbidden: %d %s", refused.Code, refused.Body.String())
	}
	fake.Answer(opnsensetest.ExportPath, http.StatusOK, opnsensetest.Configuration)
	saved := callAPI(server.Server, http.MethodPut, "/api/backups/plans/bordure", body)
	if saved.Code != http.StatusOK {
		t.Fatalf("plan: %d %s", saved.Code, saved.Body.String())
	}
	run := callAPI(server.Server, http.MethodPost, "/api/backups/plans/bordure/actions/run", map[string]any{})
	if run.Code != http.StatusCreated {
		t.Fatalf("run: %d %s", run.Code, run.Body.String())
	}

	outputs := []string{run.Body.String(), saved.Body.String(), refused.Body.String(), journal.String(), server.logs.String()}
	for _, path := range []string{"/api/backups", "/api/backups/overview", "/api/firewalls/" + firewallID} {
		outputs = append(outputs, callAPI(server.Server, http.MethodGet, path, nil).Body.String())
	}
	events, _ := server.db.ListAuditEvents(context.Background(), audit.Query{Limit: 200})
	rawEvents, _ := json.Marshal(events)
	runs, _ := server.db.ListActionRuns(context.Background(), action.Query{Limit: 50})
	rawRuns, _ := json.Marshal(runs)
	outputs = append(outputs, string(rawEvents), string(rawRuns))
	for _, seeded := range []string{opnsensetest.Key, opnsensetest.Secret, opnsensetest.ConfigurationSecret} {
		for index, output := range outputs {
			if strings.Contains(output, seeded) {
				t.Errorf("%q leaked in output %d", seeded, index)
			}
		}
	}
	if len(runs) == 0 {
		t.Fatal("no order was sent to the destination")
	}
}
