package server

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

func nextcloudRequest(machineID string) declaredServiceRequest {
	return declaredServiceRequest{
		MachineID: machineID, Name: "nextcloud", Title: "Nextcloud", Nature: "application", Origin: "compose",
		ComposeProject: "nextcloud", PublicURL: "https://cloud.exemple.fr",
		Units: []unitRequest{
			{Name: "app", Image: "nextcloud", Version: "29.0.4", Ports: []string{"8080:80"}},
			{Name: "db", Image: "postgres", Version: "16.4"},
		},
	}
}

// L'annuaire pour des réponses parlantes : un projet, une application
// Compose sur la machine distante qui en fait partie, un proxy en binaire
// qui fournit deux choses, un conteneur seul sans projet.
func (ts *testServer) seedDirectory(t *testing.T) (serviceID string) {
	t.Helper()
	ctx := context.Background()
	project, err := ts.directory.CreateProject(ctx, directory.ProjectDefinition{Name: "client-a", Title: "Client A"})
	if err != nil {
		t.Fatal(err)
	}
	cloud := declaredServiceDefinition(nextcloudRequest(remoteID))
	cloud.ProjectIDs = []string{project.ID}
	created, err := ts.directory.CreateService(ctx, cloud)
	if err != nil {
		t.Fatal(err)
	}
	proxy := directory.ServiceDefinition{
		MachineID: machine.LocalID, Name: "caddy", Nature: directory.NatureInfra, Origin: directory.OriginBinary,
		Provides: []directory.Capability{directory.CapabilityTLS, directory.CapabilityProxy},
		Units:    []directory.UnitDefinition{{Name: "caddy", Version: "2.8.4", SystemdUnit: "caddy.service", Ports: []string{"80/tcp", "443"}}},
	}
	if _, err := ts.directory.CreateService(ctx, proxy); err != nil {
		t.Fatal(err)
	}
	whoami := directory.ServiceDefinition{
		MachineID: remoteID, Name: "whoami", Nature: directory.NatureApplication, Origin: directory.OriginContainer,
		Units: []directory.UnitDefinition{{Name: "whoami", Image: "traefik/whoami", Version: "v1.10", ContainerName: "whoami"}},
	}
	if _, err := ts.directory.CreateService(ctx, whoami); err != nil {
		t.Fatal(err)
	}
	return created.ID
}

func TestDirectoryAPI_ReadsMatchGoldenFiles(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	serviceID := server.seedDirectory(t)
	cases := map[string]string{
		"declared_services": "/api/directory/services",
		"declared_service":  "/api/directory/services/" + serviceID,
		"projects":          "/api/directory/projects",
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

func TestDirectoryAPI_FiltersByMachineAndProject(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	server.seedDirectory(t)
	var onLocal declaredServicesResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/directory/services?machine=local", nil), &onLocal)
	if len(onLocal.Services) != 1 || onLocal.Services[0].Name != "caddy" {
		t.Fatalf("machine locale: %+v", onLocal.Services)
	}
	var projects projectsResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/directory/projects", nil), &projects)
	var inProject declaredServicesResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/directory/services?project="+projects.Projects[0].ID, nil), &inProject)
	if len(inProject.Services) != 1 || inProject.Services[0].Name != "nextcloud" {
		t.Fatalf("projet: %+v", inProject.Services)
	}
}

func TestAPI_CreateDeclaredService_RefusesWithTheKey(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	if got := callAPI(server.Server, http.MethodPost, "/api/directory/services", nextcloudRequest(remoteID)); got.Code != http.StatusCreated {
		t.Fatalf("création: %d %s", got.Code, got.Body.String())
	}
	cases := []struct {
		name  string
		build func(declaredServiceRequest) declaredServiceRequest
		key   string
	}{
		{"nom pris", func(r declaredServiceRequest) declaredServiceRequest { r.ComposeProject = "autre"; return r }, "directory.name_taken"},
		{"projet Compose pris", func(r declaredServiceRequest) declaredServiceRequest { r.Name = "autre"; return r }, "directory.compose_project_taken"},
		{"version latest", func(r declaredServiceRequest) declaredServiceRequest {
			r.Name, r.ComposeProject = "autre", "autre"
			r.Units[0].Version = "latest"
			return r
		}, "directory.version_latest"},
		{"fourniture sur une application", func(r declaredServiceRequest) declaredServiceRequest {
			r.Name, r.ComposeProject = "autre", "autre"
			r.Provides = []string{"proxy"}
			return r
		}, "directory.provides_need_infra"},
		{"machine inconnue", func(r declaredServiceRequest) declaredServiceRequest {
			r.MachineID = "inconnue"
			return r
		}, "directory.machine_invalid"},
		{"projet inconnu", func(r declaredServiceRequest) declaredServiceRequest {
			r.Name, r.ComposeProject = "autre", "autre"
			r.ProjectIDs = []string{"prj_inconnu"}
			return r
		}, "directory.project_invalid"},
		{"nom illisible", func(r declaredServiceRequest) declaredServiceRequest { r.Name = "Nextcloud"; return r }, "directory.name_invalid"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := callAPI(server.Server, http.MethodPost, "/api/directory/services", testCase.build(nextcloudRequest(remoteID)))
			if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != testCase.key {
				t.Fatalf("%d %s, attendu 422 %s", got.Code, got.Body.String(), testCase.key)
			}
		})
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/directory/services", map[string]any{"inconnu": 1}); got.Code != http.StatusBadRequest {
		t.Fatalf("champ inconnu: %d", got.Code)
	}
}

func TestAPI_UpdateDeclaredService_KeepsUnitIDAndAnswersNotFound(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	var created declaredServiceJSON
	decodeAPI(t, callAPI(server.Server, http.MethodPost, "/api/directory/services", nextcloudRequest(remoteID)), &created)

	request := nextcloudRequest(remoteID)
	request.Units = []unitRequest{{ID: created.Units[0].ID, Name: "app", Image: "nextcloud", Version: "29.0.6"}}
	got := callAPI(server.Server, http.MethodPut, "/api/directory/services/"+created.ID, request)
	if got.Code != http.StatusOK {
		t.Fatalf("édition: %d %s", got.Code, got.Body.String())
	}
	var updated declaredServiceJSON
	decodeAPI(t, got, &updated)
	if len(updated.Units) != 1 || updated.Units[0].ID != created.Units[0].ID || updated.Units[0].Version != "29.0.6" {
		t.Fatalf("unités %+v", updated.Units)
	}

	twice := nextcloudRequest(remoteID)
	twice.Units = []unitRequest{
		{ID: created.Units[0].ID, Name: "app", Image: "nextcloud", Version: "29.0.6"},
		{ID: created.Units[0].ID, Name: "db", Image: "postgres", Version: "16.4"},
	}
	if got := callAPI(server.Server, http.MethodPut, "/api/directory/services/"+created.ID, twice); got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != "directory.unit_invalid" {
		t.Fatalf("même identifiant d'unité deux fois: %d %s", got.Code, got.Body.String())
	}

	if got := callAPI(server.Server, http.MethodPut, "/api/directory/services/svc_inconnu", request); got.Code != http.StatusNotFound {
		t.Fatalf("service inconnu: %d", got.Code)
	}
	if got := callAPI(server.Server, http.MethodDelete, "/api/directory/services/svc_inconnu", nil); got.Code != http.StatusNotFound {
		t.Fatalf("suppression d'un inconnu: %d", got.Code)
	}
	if got := callAPI(server.Server, http.MethodDelete, "/api/directory/services/"+created.ID, nil); got.Code != http.StatusNoContent {
		t.Fatalf("suppression: %d", got.Code)
	}
}

func TestAPI_Projects_CreateRefuseAndDeleteKeepsServices(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	var project projectJSON
	got := callAPI(server.Server, http.MethodPost, "/api/directory/projects", projectRequest{Name: "client-a", Title: "Client A"})
	if got.Code != http.StatusCreated {
		t.Fatalf("création: %d %s", got.Code, got.Body.String())
	}
	decodeAPI(t, got, &project)

	refusals := []struct {
		name    string
		request projectRequest
		key     string
	}{
		{"nom pris", projectRequest{Name: "client-a"}, "project.name_taken"},
		{"nom illisible", projectRequest{Name: "Client A"}, "project.name_invalid"},
		{"titre trop long", projectRequest{Name: "client-b", Title: strings.Repeat("a", 81)}, "project.title_invalid"},
	}
	for _, refusal := range refusals {
		got := callAPI(server.Server, http.MethodPost, "/api/directory/projects", refusal.request)
		if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != refusal.key {
			t.Errorf("%s: %d %s, attendu %s", refusal.name, got.Code, got.Body.String(), refusal.key)
		}
	}

	request := nextcloudRequest(remoteID)
	request.ProjectIDs = []string{project.ID}
	var service declaredServiceJSON
	decodeAPI(t, callAPI(server.Server, http.MethodPost, "/api/directory/services", request), &service)

	if got := callAPI(server.Server, http.MethodDelete, "/api/directory/projects/"+project.ID, nil); got.Code != http.StatusNoContent {
		t.Fatalf("suppression du projet: %d", got.Code)
	}
	kept := callAPI(server.Server, http.MethodGet, "/api/directory/services/"+service.ID, nil)
	if kept.Code != http.StatusOK {
		t.Fatalf("le service a suivi son projet: %d", kept.Code)
	}
	if got := callAPI(server.Server, http.MethodGet, "/api/directory/projects/"+project.ID, nil); got.Code != http.StatusNotFound {
		t.Fatalf("projet supprimé: %d", got.Code)
	}
}

func TestAPI_RemoveMachine_WithDeclaredServices_Answers409(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	var created declaredServiceJSON
	decodeAPI(t, callAPI(server.Server, http.MethodPost, "/api/directory/services", nextcloudRequest(remoteID)), &created)

	got := callAPI(server.Server, http.MethodDelete, "/api/machines/"+remoteID, nil)
	if got.Code != http.StatusConflict || errorCode(t, got) != "machine.has_declared_services" {
		t.Fatalf("%d %s", got.Code, got.Body.String())
	}
	if got := callAPI(server.Server, http.MethodGet, "/api/machines/"+remoteID, nil); got.Code != http.StatusOK {
		t.Fatalf("la machine a disparu malgré le refus: %d", got.Code)
	}

	callAPI(server.Server, http.MethodDelete, "/api/directory/services/"+created.ID, nil)
	if got := callAPI(server.Server, http.MethodDelete, "/api/machines/"+remoteID, nil); got.Code != http.StatusNoContent {
		t.Fatalf("machine sans service: %d %s", got.Code, got.Body.String())
	}
}

// Les clés des refus sont composées par directoryRefusalKey et
// projectRefusalKey : le garde-fou des appels en dur ne les voit pas.
func TestDirectory_RefusalKeysExistInEveryCatalog(t *testing.T) {
	server := newTestServer(t)
	refusals := []error{
		directory.ErrMachineInvalid, directory.ErrNameInvalid, directory.ErrTitleInvalid, directory.ErrNatureInvalid,
		directory.ErrOriginInvalid, directory.ErrComposeProjectInvalid, directory.ErrPublicURLInvalid,
		directory.ErrProvidesInvalid, directory.ErrProvidesNeedInfra, directory.ErrUnitsInvalid,
		directory.ErrUnitNameInvalid, directory.ErrUnitNameTaken, directory.ErrImageInvalid, directory.ErrVersionInvalid,
		directory.ErrVersionLatest, directory.ErrDigestInvalid, directory.ErrContainerNameInvalid,
		directory.ErrSystemdUnitInvalid, directory.ErrPortsInvalid, directory.ErrNameTaken,
		directory.ErrComposeProjectTaken, directory.ErrMatchKeyTaken, directory.ErrUnitInvalid, directory.ErrProjectInvalid, directory.ErrUnitKeyDuplicate,
		directory.ErrIgnoreInvalid,
	}
	for _, refusal := range refusals {
		key, refused := directoryRefusalKey(refusal)
		if !refused {
			t.Errorf("%v n'a pas de clé", refusal)
			continue
		}
		requireKeyInEveryCatalog(t, server, key)
	}
	for _, refusal := range []error{directory.ErrProjectNameInvalid, directory.ErrProjectTitleInvalid, directory.ErrProjectNameTaken} {
		key, refused := projectRefusalKey(refusal)
		if !refused {
			t.Errorf("%v n'a pas de clé", refusal)
			continue
		}
		requireKeyInEveryCatalog(t, server, key)
	}
}

func requireKeyInEveryCatalog(t *testing.T, server *testServer, key string) {
	t.Helper()
	for _, code := range lang.Codes() {
		if strings.HasPrefix(server.catalogs.For(code).Get(key), "[") {
			t.Errorf("la clé %q manque dans %s", key, code)
		}
	}
}
