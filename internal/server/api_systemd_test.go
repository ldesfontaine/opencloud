package server

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/goldentest"
	"github.com/ldesfontaine/opencloud/internal/systemdunits"
)

func systemdUnit(name string, active systemdunits.Active, sub systemdunits.Sub) systemdunits.Unit {
	return systemdunits.Unit{Name: name, Load: systemdunits.LoadLoaded, Active: active, Sub: sub}
}

// connectedAgent enrôle la machine distante et ouvre la session de son agent.
func (ts *testServer) connectedAgent(t *testing.T) string {
	t.Helper()
	enrolled, _ := ts.enroll(t, "vps-paris-1", remoteID)
	session, err := ts.machines.Connect(context.Background(), enrolled, "192.0.2.1", "v0.0.1", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return session.Token
}

// Le signal de l'agent écrit le relevé de sa machine, que l'API rend avec le
// service déclaré qui porte chaque unité ; la fiche du service dit l'état de
// son unité binaire, et l'écart la juge.
func TestSystemd_TheSignalWritesTheReportAndTheAPIReadsItWithTheDeclaredService(t *testing.T) {
	server := newTestServer(t)
	token := server.connectedAgent(t)
	created, err := server.directory.CreateService(context.Background(), directory.ServiceDefinition{
		MachineID: remoteID, Name: "forgejo", Nature: directory.NatureApplication, Origin: directory.OriginBinary,
		Units: []directory.UnitDefinition{{Name: "forgejo", SystemdUnit: "forgejo.service"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	report := systemdunits.Report{Available: true, Units: []systemdunits.Unit{
		systemdUnit("forgejo.service", systemdunits.ActiveActive, systemdunits.SubRunning),
		systemdUnit("ssh.service", systemdunits.ActiveActive, systemdunits.SubRunning),
		systemdUnit("backup.service", systemdunits.ActiveFailed, systemdunits.SubFailed),
	}}

	if got := server.agentPost(token, "/agent/signal", agentwire.SignalRequest{SystemdUnits: &report}); got.Code != http.StatusNoContent {
		t.Fatalf("signal: %d %s", got.Code, got.Body.String())
	}

	machine := callAPI(server.Server, http.MethodGet, "/api/machines/"+remoteID+"/systemd-units", nil)
	if machine.Code != http.StatusOK {
		t.Fatalf("machine: %d %s", machine.Code, machine.Body.String())
	}
	goldentest.Compare(t, filepath.Join("testdata", "systemd_units_machine.golden.json"), normalize(machine.Body.String()))

	service := callAPI(server.Server, http.MethodGet, "/api/directory/services/"+created.ID, nil)
	var detail struct {
		Units []struct {
			Observed *struct {
				Presence string `json:"presence"`
				Active   string `json:"active"`
				Sub      string `json:"sub"`
			} `json:"observed"`
		} `json:"units"`
	}
	if err := json.Unmarshal(service.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if observed := detail.Units[0].Observed; observed == nil || observed.Presence != "found" || observed.Active != "active" || observed.Sub != "running" {
		t.Errorf("unité observée : %s", service.Body.String())
	}
	gaps := callAPI(server.Server, http.MethodGet, "/api/directory/gaps", nil)
	if !strings.Contains(gaps.Body.String(), `"state":"conform"`) || strings.Contains(gaps.Body.String(), `"state":"unobserved"`) {
		t.Errorf("une unité binaire trouvée est conforme : %s", gaps.Body.String())
	}
}

func TestSystemd_AMachineThatReportedNothingYetReadsEmptyAndUndated(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)

	got := callAPI(server.Server, http.MethodGet, "/api/machines/"+remoteID+"/systemd-units", nil)

	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"reported_at":null`) || !strings.Contains(got.Body.String(), `"units":[]`) || !strings.Contains(got.Body.String(), `"available":false`) {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
}

func TestSystemd_AnUnknownMachineIsNotFound(t *testing.T) {
	server := newTestServer(t)
	if got := callAPI(server.Server, http.MethodGet, "/api/machines/inconnue/systemd-units", nil); got.Code != http.StatusNotFound {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
}

// Un relevé hors bornes est refusé en entier, comme une autre section : le
// signal vit, l'ancien relevé reste.
func TestSystemd_AnInvalidReportIsRefusedAloneAndTheOldOneStays(t *testing.T) {
	server := newTestServer(t)
	token := server.connectedAgent(t)
	good := systemdunits.Report{Available: true, Units: []systemdunits.Unit{systemdUnit("forgejo.service", systemdunits.ActiveActive, systemdunits.SubRunning)}}
	if got := server.agentPost(token, "/agent/signal", agentwire.SignalRequest{SystemdUnits: &good}); got.Code != http.StatusNoContent {
		t.Fatalf("signal: %d %s", got.Code, got.Body.String())
	}
	hostile := map[string]systemdunits.Report{
		"nom":    {Available: true, Units: []systemdunits.Unit{systemdUnit("../../etc/shadow.service", systemdunits.ActiveActive, systemdunits.SubRunning)}},
		"état":   {Available: true, Units: []systemdunits.Unit{systemdUnit("a.service", "PASSWORD=x", systemdunits.SubRunning)}},
		"raison": {Available: false, Reason: "perdu", Units: []systemdunits.Unit{}},
	}
	for name, report := range hostile {
		t.Run(name, func(t *testing.T) {
			got := server.agentPost(token, "/agent/signal", agentwire.SignalRequest{SystemdUnits: &report})
			if got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "bad_systemd_units") {
				t.Fatalf("relevé hostile accepté : %d %s", got.Code, got.Body.String())
			}
		})
	}
	one := callAPI(server.Server, http.MethodGet, "/api/machines/"+remoteID+"/systemd-units", nil)
	if !strings.Contains(one.Body.String(), "forgejo.service") || strings.Contains(one.Body.String(), "shadow") {
		t.Fatalf("le relevé a changé : %s", one.Body.String())
	}
}

// Une unité relevée se déclare par la proposition : le formulaire part du nom
// et de l'unité, sans port (le relevé ne le dit pas).
func TestSystemd_ProposalForAReportedUnit_AndItsRefusals(t *testing.T) {
	server := newTestServer(t)
	token := server.connectedAgent(t)
	report := systemdunits.Report{Available: true, Units: []systemdunits.Unit{systemdUnit("forgejo.service", systemdunits.ActiveActive, systemdunits.SubRunning)}}
	if got := server.agentPost(token, "/agent/signal", agentwire.SignalRequest{SystemdUnits: &report}); got.Code != http.StatusNoContent {
		t.Fatalf("signal: %d", got.Code)
	}

	proposal := callAPI(server.Server, http.MethodGet, "/api/directory/proposal?machine="+remoteID+"&unit=forgejo.service", nil)
	if proposal.Code != http.StatusOK {
		t.Fatalf("proposition : %d %s", proposal.Code, proposal.Body.String())
	}
	goldentest.Compare(t, filepath.Join("testdata", "proposal_systemd_unit.golden.json"), normalize(proposal.Body.String()))

	gone := callAPI(server.Server, http.MethodGet, "/api/directory/proposal?machine="+remoteID+"&unit=inconnue.service", nil)
	if gone.Code != http.StatusNotFound || !strings.Contains(gone.Body.String(), "directory.systemd_unit_not_observed") {
		t.Errorf("unité absente : %d %s", gone.Code, gone.Body.String())
	}

	// Acceptée telle quelle, la proposition donne un service ; la même unité n'est plus proposée.
	var definition map[string]any
	if err := json.Unmarshal(proposal.Body.Bytes(), &struct {
		Definition *map[string]any `json:"definition"`
	}{&definition}); err != nil {
		t.Fatal(err)
	}
	created := callAPI(server.Server, http.MethodPost, "/api/directory/services", definition)
	if created.Code != http.StatusCreated {
		t.Fatalf("création : %d %s", created.Code, created.Body.String())
	}
	again := callAPI(server.Server, http.MethodGet, "/api/directory/proposal?machine="+remoteID+"&unit=forgejo.service", nil)
	if again.Code != http.StatusConflict || !strings.Contains(again.Body.String(), "directory.systemd_unit_declared") {
		t.Errorf("unité déjà déclarée : %d %s", again.Code, again.Body.String())
	}
}

// Le banc de non-fuite, de bout en bout : un secret semé dans la sortie de
// systemctl ne ressort ni de l'API (machine, services, écart), ni du signal
// que l'agent construit, ni de la base.
func TestSystemd_SeededSecret_NeverReachesTheServerOrItsAPI(t *testing.T) {
	const secret = "hunter2-S3CR3T-token"
	server := newTestServer(t)
	token := server.connectedAgent(t)
	output := "forgejo.service loaded active running Forgejo PASSWORD=" + secret + "\n" +
		"app.service loaded active running --token=" + secret + " Environment=KEY=" + secret + "\n" +
		"odd.service loaded active state-" + secret + " d\n"
	report := systemdunits.Parse([]byte(output))

	sent, err := json.Marshal(agentwire.SignalRequest{SystemdUnits: &report})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sent), secret) {
		t.Fatalf("le signal porte le secret : %s", sent)
	}
	if got := server.agentPost(token, "/agent/signal", agentwire.SignalRequest{SystemdUnits: &report}); got.Code != http.StatusNoContent {
		t.Fatalf("signal: %d %s", got.Code, got.Body.String())
	}
	for _, path := range []string{"/api/machines/" + remoteID + "/systemd-units", "/api/directory/services", "/api/directory/gaps", "/api/machines", "/api/directory/proposal?machine=" + remoteID + "&unit=forgejo.service"} {
		got := callAPI(server.Server, http.MethodGet, path, nil)
		if strings.Contains(got.Body.String(), secret) || strings.Contains(got.Body.String(), "PASSWORD") {
			t.Errorf("%s rend le secret : %s", path, got.Body.String())
		}
	}
	stored, err := server.db.MachineSystemdUnits(context.Background(), remoteID)
	if err != nil {
		t.Fatal(err)
	}
	kept, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(kept), secret) {
		t.Errorf("la base garde le secret : %s", kept)
	}
}
