package server

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/service"
)

// L'annuaire face à ce que seedServices montre sur la machine distante :
// nextcloud est déclaré avec deux unités, l'application qui tourne à la
// version déclarée et un redis qui ne tourne pas ; nextcloud-db tourne dans
// ce projet sans être déclaré. Un binaire sur la machine openCloud,
// qu'aucun agent ne regarde.
func (ts *testServer) seedGaps(t *testing.T) (serviceID string) {
	t.Helper()
	ctx := context.Background()
	cloud, err := ts.directory.CreateService(ctx, directory.ServiceDefinition{
		MachineID: remoteID, Name: "nextcloud", Title: "Nextcloud", Nature: directory.NatureApplication,
		Origin: directory.OriginCompose, ComposeProject: "nextcloud",
		Units: []directory.UnitDefinition{
			{Name: "nextcloud", Image: "nextcloud", Version: "29.0.4", Ports: []string{"8080:80"}},
			{Name: "redis", Image: "redis", Version: "7.4"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	backup := directory.ServiceDefinition{
		MachineID: machine.LocalID, Name: "backup", Nature: directory.NatureInfra, Origin: directory.OriginBinary,
		Units: []directory.UnitDefinition{{Name: "restic", Version: "0.17.1", SystemdUnit: "restic.service"}},
	}
	if _, err := ts.directory.CreateService(ctx, backup); err != nil {
		t.Fatal(err)
	}
	return cloud.ID
}

func nextcloudContainerID() string   { return service.ID(remoteID, strings.Repeat("a", 64)) }
func nextcloudDBContainerID() string { return service.ID(remoteID, strings.Repeat("b", 64)) }

func TestDirectoryAPI_GapsAndProposalMatchGoldenFiles(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	server.seedServices(t)
	server.seedGaps(t)
	cases := map[string]string{
		"directory_gaps":     "/api/directory/gaps",
		"directory_proposal": "/api/directory/proposal?container=" + nextcloudDBContainerID(),
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			recorder := callAPI(server.Server, http.MethodGet, path, nil)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
			}
			compareGolden(t, filepath.Join("testdata", name+".golden.json"), normalize(recorder.Body.String()))
		})
	}
}

func TestDirectoryAPI_GapsFilterByMachineAndProject(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	server.seedServices(t)
	server.seedGaps(t)
	project, err := server.directory.CreateProject(context.Background(), directory.ProjectDefinition{Name: "client-a"})
	if err != nil {
		t.Fatal(err)
	}

	var onLocal gapsResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/directory/gaps?machine=local", nil), &onLocal)
	if len(onLocal.Services) != 1 || onLocal.Services[0].State != "unobserved" || len(onLocal.Undeclared) != 0 {
		t.Fatalf("machine openCloud: %+v", onLocal)
	}
	var onRemote gapsResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/directory/gaps?machine="+remoteID, nil), &onRemote)
	if len(onRemote.Services) != 1 || onRemote.Services[0].State != "absent" || len(onRemote.Undeclared) != 1 {
		t.Fatalf("machine distante: %+v", onRemote)
	}
	var inProject gapsResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/directory/gaps?project="+project.ID, nil), &inProject)
	if len(inProject.Services) != 0 || inProject.Undeclared == nil || len(inProject.Undeclared) != 0 {
		t.Fatalf("projet vide: %+v", inProject)
	}
}

func TestAPI_Ignore_RefusesWithTheKey(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	server.seedServices(t)
	server.seedGaps(t)

	refusals := []struct {
		name string
		body ignoredRequest
		key  string
	}{
		{"genre inconnu", ignoredRequest{MachineID: remoteID, Kind: "id", Value: "nextcloud"}, "directory.ignore_invalid"},
		{"valeur illisible", ignoredRequest{MachineID: remoteID, Kind: "compose_project", Value: "Next Cloud"}, "directory.ignore_invalid"},
		{"machine inconnue", ignoredRequest{MachineID: "inconnue", Kind: "container_name", Value: "whoami"}, "directory.machine_invalid"},
	}
	for _, refusal := range refusals {
		t.Run(refusal.name, func(t *testing.T) {
			got := callAPI(server.Server, http.MethodPost, "/api/directory/ignored", refusal.body)
			if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != refusal.key {
				t.Fatalf("%d %s, attendu 422 %s", got.Code, got.Body.String(), refusal.key)
			}
		})
	}

	body := ignoredRequest{MachineID: remoteID, Kind: "compose_project", Value: "nextcloud"}
	for range 2 {
		if got := callAPI(server.Server, http.MethodPost, "/api/directory/ignored", body); got.Code != http.StatusNoContent {
			t.Fatalf("ignorer (deux fois): %d %s", got.Code, got.Body.String())
		}
	}
	var gaps gapsResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/directory/gaps?machine="+remoteID, nil), &gaps)
	if len(gaps.Undeclared) != 1 || !gaps.Undeclared[0].Ignored {
		t.Fatalf("la clé ignorée est dite: %+v", gaps.Undeclared)
	}

	query := "/api/directory/ignored?machine=" + remoteID + "&kind=compose_project&value=nextcloud"
	if got := callAPI(server.Server, http.MethodDelete, query, nil); got.Code != http.StatusNoContent {
		t.Fatalf("ne plus ignorer: %d %s", got.Code, got.Body.String())
	}
	got := callAPI(server.Server, http.MethodDelete, query, nil)
	if got.Code != http.StatusNotFound || errorCode(t, got) != "directory.ignore_not_found" {
		t.Fatalf("ne plus ignorer deux fois: %d %s", got.Code, got.Body.String())
	}
}

func TestAPI_Proposal_DeclaredContainerAnswers409(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	server.seedServices(t)
	server.seedGaps(t)

	got := callAPI(server.Server, http.MethodGet, "/api/directory/proposal?container="+nextcloudContainerID(), nil)
	if got.Code != http.StatusConflict || errorCode(t, got) != "directory.container_declared" {
		t.Fatalf("conteneur déclaré: %d %s", got.Code, got.Body.String())
	}
	got = callAPI(server.Server, http.MethodGet, "/api/directory/proposal?container=inconnu", nil)
	if got.Code != http.StatusNotFound || errorCode(t, got) != "directory.container_not_found" {
		t.Fatalf("conteneur inconnu: %d %s", got.Code, got.Body.String())
	}
	got = callAPI(server.Server, http.MethodGet, "/api/directory/proposal", nil)
	if got.Code != http.StatusNotFound {
		t.Fatalf("sans conteneur: %d", got.Code)
	}
}

// Un service déjà plein ne reçoit plus d'unité : la réponse le dit, plutôt
// que de rendre une définition inchangée.
func TestAPI_Proposal_FullServiceAnswers409(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	server.seedServices(t)
	serviceID := server.seedGaps(t)

	ctx := context.Background()
	current, err := server.directory.GetService(ctx, serviceID)
	if err != nil {
		t.Fatal(err)
	}
	definition := directory.ServiceDefinition{
		MachineID: current.MachineID, Name: current.Name, Title: current.Title, Nature: current.Nature,
		Origin: current.Origin, ComposeProject: current.ComposeProject,
	}
	for _, unit := range current.Units {
		definition.Units = append(definition.Units, directory.UnitDefinition{ID: unit.ID, Name: unit.Name, Image: unit.Image, Version: unit.Version, Ports: unit.Ports})
	}
	for index := len(definition.Units); index < directory.MaxUnits; index++ {
		definition.Units = append(definition.Units, directory.UnitDefinition{Name: "extra-" + strconv.Itoa(index), Image: "busybox", Version: "1"})
	}
	if _, err := server.directory.UpdateService(ctx, serviceID, definition); err != nil {
		t.Fatal(err)
	}

	got := callAPI(server.Server, http.MethodGet, "/api/directory/proposal?container="+nextcloudDBContainerID(), nil)
	if got.Code != http.StatusConflict || errorCode(t, got) != "directory.units_full" {
		t.Fatalf("service plein: %d %s", got.Code, got.Body.String())
	}
}

// Une proposition d'adoption d'un projet déjà déclaré est une édition : elle
// doit passer telle quelle par PUT, ids d'unités existantes compris.
func TestAPI_Proposal_CanBeSentBackAsTheEdit(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	server.seedServices(t)
	serviceID := server.seedGaps(t)

	var proposal proposalResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/directory/proposal?container="+nextcloudDBContainerID(), nil), &proposal)
	if proposal.ServiceID != serviceID || len(proposal.Definition.Units) != 3 {
		t.Fatalf("proposition: %+v", proposal)
	}
	for index, unit := range proposal.Definition.Units {
		if (index < 2) != (unit.ID != "") {
			t.Fatalf("seules les unités déjà déclarées portent un id: %+v", proposal.Definition.Units)
		}
	}
	got := callAPI(server.Server, http.MethodPut, "/api/directory/services/"+serviceID, proposal.Definition)
	if got.Code != http.StatusOK {
		t.Fatalf("édition: %d %s", got.Code, got.Body.String())
	}
	var gaps gapsResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/directory/gaps?machine="+remoteID, nil), &gaps)
	if len(gaps.Undeclared) != 0 {
		t.Fatalf("le conteneur adopté n'est plus non déclaré: %+v", gaps.Undeclared)
	}
	if unit := gaps.Services[0].Units[2]; unit.Name != "nextcloud-db" || unit.Containers[0].MatchLevel != "compose" {
		t.Fatalf("unités: %+v", gaps.Services[0].Units)
	}
}

func TestAPI_ServiceSheet_SaysWhatTheDirectoryKnows(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	server.seedServices(t)
	server.seedGaps(t)

	var declared serviceResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/services/"+nextcloudContainerID(), nil), &declared)
	link := declared.Directory
	if link == nil || !link.Declared || link.ServiceName != "nextcloud" || link.UnitName != "nextcloud" || link.MatchLevel != "compose" || link.Weak {
		t.Fatalf("rattaché: %+v", link)
	}

	var undeclared serviceResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/services/"+nextcloudDBContainerID(), nil), &undeclared)
	link = undeclared.Directory
	if link == nil || link.Declared || link.KeyKind != "compose_project" || link.KeyValue != "nextcloud" || link.Ignored || link.ServiceID != "" {
		t.Fatalf("non rattaché: %+v", link)
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/directory/ignored", ignoredRequest{MachineID: remoteID, Kind: "compose_project", Value: "nextcloud"}); got.Code != http.StatusNoContent {
		t.Fatalf("ignorer: %d", got.Code)
	}
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/services/"+nextcloudDBContainerID(), nil), &undeclared)
	if !undeclared.Directory.Ignored {
		t.Fatalf("ignoré: %+v", undeclared.Directory)
	}

	// Les listes ne portent pas le champ : leurs réponses figées n'ont pas bougé.
	if got := callAPI(server.Server, http.MethodGet, "/api/services", nil); strings.Contains(got.Body.String(), `"directory"`) {
		t.Fatalf("la liste des services porte l'annuaire: %s", got.Body.String())
	}
}
