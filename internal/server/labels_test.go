package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
)

func labelledObserved(id, name string, labels map[string]string) container.Observed {
	return container.Observed{
		ContainerID: id, Name: name, Image: "traefik/whoami:v1.10", State: container.StateRunning,
		CreatedAt: testNow.Add(-time.Hour), Labels: labels,
	}
}

// recordOnRemote ajoute des conteneurs à ceux du seed commun, qui restent : un
// démarrage par conteneur plutôt qu'un inventaire complet, qui les archiverait.
func (ts *testServer) recordOnRemote(t *testing.T, observed ...container.Observed) {
	t.Helper()
	var report container.Report
	for index := range observed {
		report.Events = append(report.Events, container.Event{
			At: testNow, Action: "start", ContainerID: observed[index].ContainerID, State: container.StateRunning, Container: &observed[index],
		})
	}
	if err := ts.containers.Record(context.Background(), remoteID, report); err != nil {
		t.Fatal(err)
	}
}

// Un serveur plus ancien lit un signal d'un agent plus récent : un champ qu'il
// ne connaît pas est ignoré, le reste passe. C'est de cette tolérance, que le
// décodeur du signal tient de ne pas refuser les champs inconnus, que dépend
// l'envoi des labels par un agent à jour vers un serveur qui ne les lit pas.
func TestAgentSignal_UnknownFieldInAContainerIsIgnored(t *testing.T) {
	server := newTestServer(t)
	enrolled, private := server.enroll(t, "vps-paris-1", remoteID)
	resp, session, _, cancel := openStream(t, server, enrolled.ID, private)
	defer resp.Body.Close()
	defer cancel()

	created := testNow.Add(-time.Hour).Format(time.RFC3339)
	body := `{
		"champ_futur_du_signal": {"a": 1},
		"services": {
			"engine": {"present": true},
			"complete": true,
			"champ_futur_du_rapport": [1, 2],
			"inventory": [{
				"container_id": "` + strings.Repeat("a", 64) + `", "name": "web", "image": "nginx:1.27",
				"state": "running", "exit_code": 0, "restart_count": 0, "created_at": "` + created + `",
				"labels": {"service": "svc_0123456789abcdef", "unit": "app"},
				"champ_futur_du_conteneur": {"nested": true}
			}]
		}
	}`
	request := httptest.NewRequest(http.MethodPost, "/agent/signal", bytes.NewBufferString(body))
	request.Header.Set("Authorization", "Bearer "+session)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("un champ inconnu doit être ignoré : %d %s", recorder.Code, recorder.Body.String())
	}
	found, err := server.db.GetContainer(context.Background(), container.ID(remoteID, strings.Repeat("a", 64)))
	if err != nil || found.Name != "web" {
		t.Fatalf("le conteneur du signal doit être écrit : %+v, %v", found, err)
	}
	if found.Labels != (container.Labels{Service: "svc_0123456789abcdef", Unit: "app"}) {
		t.Fatalf("labels = %+v", found.Labels)
	}
}

// Assertions directes sur l'API : un conteneur étiqueté vers une unité valide
// se rattache par son label, les autres restent non déclarés avec leur motif,
// et rien ne change dans l'annuaire.
func TestDirectoryAPI_LabelMatchAndOrphan(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	server.seedContainers(t)
	cloudID := server.seedGaps(t)
	services, err := server.db.ListDeclaredServices(context.Background(), directory.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	var backupID string
	for _, found := range services {
		if found.Name == "backup" {
			backupID = found.ID
		}
	}
	caddy, err := server.directory.CreateService(context.Background(), directory.ServiceDefinition{
		MachineID: remoteID, Name: "caddy", Nature: directory.NatureInfra, Origin: directory.OriginBinary,
		Units: []directory.UnitDefinition{{Name: "caddy", Version: "2.8.4", SystemdUnit: "caddy.service"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := server.db.ListDeclaredServices(context.Background(), directory.Filter{})
	if err != nil {
		t.Fatal(err)
	}

	ids := map[string]string{
		"tagged": strings.Repeat("e", 64), "typo": strings.Repeat("f", 64), "elsewhere": strings.Repeat("9", 64),
		"half": strings.Repeat("8", 64), "binary": strings.Repeat("7", 64), "plain": strings.Repeat("6", 64),
		"nounit": strings.Repeat("5", 64), "malformed": strings.Repeat("4", 64),
	}
	server.recordOnRemote(t,
		labelledObserved(ids["tagged"], "worker", map[string]string{"service": cloudID, "unit": "redis"}),
		labelledObserved(ids["typo"], "typo", map[string]string{"service": "svc_0000000000000000", "unit": "redis"}),
		labelledObserved(ids["elsewhere"], "elsewhere", map[string]string{"service": backupID, "unit": "restic"}),
		labelledObserved(ids["half"], "half", map[string]string{"service": cloudID}),
		labelledObserved(ids["binary"], "binary", map[string]string{"service": caddy.ID, "unit": "caddy"}),
		labelledObserved(ids["nounit"], "nounit", map[string]string{"service": cloudID, "unit": "pas-une-unite"}),
		labelledObserved(ids["malformed"], "malformed", map[string]string{"service": "pas un identifiant", "unit": "redis"}),
		labelledObserved(ids["plain"], "plain", nil),
	)

	var gaps gapsResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/directory/gaps", nil), &gaps)

	// Rattaché par son label, ni faible ni sous Compose.
	var redis *unitGapJSON
	for _, gap := range gaps.Services {
		if gap.ServiceID == cloudID {
			for index := range gap.Units {
				if gap.Units[index].Name == "redis" {
					redis = &gap.Units[index]
				}
			}
		}
	}
	if redis == nil || len(redis.Containers) != 1 || redis.Containers[0].Name != "worker" || redis.Containers[0].MatchLevel != "label" || redis.Weak {
		t.Fatalf("redis = %+v", redis)
	}

	problems := map[string]string{}
	adoptable := map[string]bool{}
	for _, group := range gaps.Undeclared {
		for _, found := range group.Containers {
			problems[found.Name] = found.LabelProblem
			adoptable[found.Name] = group.Adoptable
			if found.Name == "worker" {
				t.Errorf("le conteneur rattaché par son label ne peut pas être non déclaré : %+v", group)
			}
		}
	}
	for name, want := range map[string]string{
		"typo": "service_unknown", "elsewhere": "service_unknown", "half": "incomplete", "binary": "unit_binary", "nounit": "unit_unknown", "malformed": "invalid", "plain": "",
	} {
		if got, listed := problems[name]; !listed || got != want {
			t.Errorf("%s : label_problem = %q (listé : %v), want %q", name, got, listed, want)
		}
	}
	if adoptable["typo"] || !adoptable["plain"] {
		t.Errorf("adoptable = %v : un orphelin ne s'adopte pas, un conteneur sans label oui", adoptable)
	}

	// La fiche dit où est déclaré le service visé quand c'est une autre machine.
	var card containerResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/containers/"+container.ID(remoteID, ids["elsewhere"]), nil), &card)
	if card.Directory == nil || card.Directory.Declared || card.Directory.LabelProblem != "service_unknown" ||
		card.Directory.LabelMachineName != brand.ID || card.Directory.LabelService != backupID || card.Directory.LabelUnit != "restic" {
		t.Fatalf("fiche = %+v", card.Directory)
	}
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/containers/"+container.ID(remoteID, ids["tagged"]), nil), &card)
	if card.Directory == nil || !card.Directory.Declared || card.Directory.MatchLevel != "label" || card.Directory.Weak || card.Directory.UnitName != "redis" || card.Directory.LabelProblem != "" {
		t.Fatalf("fiche rattachée = %+v", card.Directory)
	}

	after, err := server.db.ListDeclaredServices(context.Background(), directory.Filter{})
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("l'annuaire a changé : %v\n%+v\n%+v", err, before, after)
	}
}

func TestAPI_Proposal_LabelledContainerAnswers409(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	server.seedContainers(t)
	server.seedGaps(t)
	id := strings.Repeat("e", 64)
	server.recordOnRemote(t, labelledObserved(id, "typo", map[string]string{"service": "svc_0000000000000000", "unit": "x"}))

	got := callAPI(server.Server, http.MethodGet, "/api/directory/proposal?container="+container.ID(remoteID, id), nil)
	if got.Code != http.StatusConflict || errorCode(t, got) != "directory.container_labelled" {
		t.Fatalf("conteneur étiqueté : %d %s", got.Code, got.Body.String())
	}
}

// seedProposals ajoute à ce que le seed commun montre un conteneur seul, que
// l'export propose de déclarer, et un conteneur orphelin, qu'il ne propose pas ;
// nextcloud-db, du projet déclaré, devient une unité proposée.
func (ts *testServer) seedProposals(t *testing.T) {
	t.Helper()
	ts.recordOnRemote(t,
		labelledObserved(strings.Repeat("5", 64), "front", nil),
		labelledObserved(strings.Repeat("4", 64), "typo", map[string]string{"service": "svc_0000000000000000", "unit": "typo"}),
	)
}

func TestManifestAPI_ExportProposeMatchesGolden(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	server.seedContainers(t)
	server.seedGaps(t)
	server.seedProposals(t)

	plain := callAPI(server.Server, http.MethodGet, "/api/manifest/export", nil)
	proposed := callAPI(server.Server, http.MethodGet, "/api/manifest/export?propose=1", nil)
	if proposed.Code != http.StatusOK {
		t.Fatalf("status %d: %s", proposed.Code, proposed.Body.String())
	}
	for _, header := range []string{"Content-Type", "Content-Disposition", "Cache-Control"} {
		if proposed.Header().Get(header) != plain.Header().Get(header) {
			t.Errorf("%s : %q, want %q", header, proposed.Header().Get(header), plain.Header().Get(header))
		}
	}
	body := proposed.Body.String()
	if strings.Contains(plain.Body.String(), "relisez avant apply") || !strings.Contains(body, "relisez avant apply") {
		t.Fatalf("seul ?propose=1 ajoute des propositions :\n%s", body)
	}
	if strings.Contains(body, "typo") {
		t.Fatalf("un label orphelin ne se propose pas :\n%s", body)
	}
	compareGolden(t, filepath.Join("testdata", "manifest_export_propose.golden.yaml"), normalizeYAML(body))

	// Toute autre valeur vaut sans propositions.
	for _, value := range []string{"0", "true", "yes", ""} {
		got := callAPI(server.Server, http.MethodGet, "/api/manifest/export?propose="+value, nil)
		if got.Body.String() != plain.Body.String() {
			t.Errorf("?propose=%s ne doit rien proposer", value)
		}
	}
}

// La socket de commande sert le même fichier : opencloud export -propose y passe.
func TestControlSocket_ExportProposeIsServed(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	server.seedContainers(t)
	server.seedGaps(t)
	server.seedProposals(t)

	request := httptest.NewRequest(http.MethodGet, "http://control/api/manifest/export?propose=1", nil)
	recorder := httptest.NewRecorder()
	server.ControlHandler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "relisez avant apply") {
		t.Fatalf("socket : %d\n%s", recorder.Code, recorder.Body.String())
	}
}

func TestMCP_ExportDirectoryPropose(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	server.seedContainers(t)
	server.seedGaps(t)
	server.seedProposals(t)
	session := connectMCP(t, server.mcpServer)

	plain, isError := callTool(t, session, "export_directory", map[string]any{})
	if isError || strings.Contains(plain, "relisez avant apply") {
		t.Fatalf("sans propose : %v %s", isError, plain)
	}
	text, isError := callTool(t, session, "export_directory", map[string]any{"propose": true})
	if isError || !strings.Contains(text, "relisez avant apply") {
		t.Fatalf("avec propose : %v %s", isError, text)
	}
	compareGolden(t, filepath.Join("testdata", "mcp_export_directory_propose.golden.json"), normalize(text)+"\n")
}
