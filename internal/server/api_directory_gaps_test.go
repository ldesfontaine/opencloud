package server

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/goldentest"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// L'annuaire face à ce que seedContainers montre sur la machine distante :
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

func nextcloudContainerID() string   { return container.ID(remoteID, strings.Repeat("a", 64)) }
func nextcloudDBContainerID() string { return container.ID(remoteID, strings.Repeat("b", 64)) }

func TestDirectoryAPI_GapsAndProposalMatchGoldenFiles(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	server.signaledAt(t, remoteID, testNow)
	server.seedContainers(t)
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
			goldentest.Compare(t, filepath.Join("testdata", name+".golden.json"), normalize(recorder.Body.String()))
		})
	}
}

func TestDirectoryAPI_GapsFilterByMachineAndProject(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	server.seedContainers(t)
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
	server.seedContainers(t)
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
	server.seedContainers(t)
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

// Le service Compose d'un projet déjà déclaré est une déclaration neuve : la
// proposition s'accepte par POST, jamais en modifiant le service voisin.
func TestAPI_Proposal_OfAServiceOfADeclaredProjectIsANewService(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	server.seedContainers(t)
	server.seedGaps(t)

	var proposal proposalResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/directory/proposal?container="+nextcloudDBContainerID(), nil), &proposal)
	if len(proposal.Definition.Units) != 1 || proposal.Definition.Units[0].ID != "" || proposal.Definition.ComposeProject != "nextcloud" {
		t.Fatalf("une unité, celle du service Compose : %+v", proposal)
	}
	got := callAPI(server.Server, http.MethodPost, "/api/directory/services", proposal.Definition)
	if got.Code != http.StatusCreated {
		t.Fatalf("accepter : %d %s", got.Code, got.Body.String())
	}
	var gaps gapsResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/directory/gaps?machine="+remoteID, nil), &gaps)
	if len(gaps.Undeclared) != 0 || len(gaps.Services) != 2 {
		t.Fatalf("le conteneur accepté n'est plus non déclaré : %+v", gaps)
	}
}

func TestAPI_ServiceSheet_SaysWhatTheDirectoryKnows(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	server.seedContainers(t)
	server.seedGaps(t)

	var declared containerResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/containers/"+nextcloudContainerID(), nil), &declared)
	link := declared.Directory
	if link == nil || !link.Declared || link.ServiceName != "nextcloud" || link.UnitName != "nextcloud" || link.MatchLevel != "compose" || link.Weak {
		t.Fatalf("rattaché: %+v", link)
	}

	var undeclared containerResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/containers/"+nextcloudDBContainerID(), nil), &undeclared)
	link = undeclared.Directory
	if link == nil || link.Declared || link.KeyKind != "compose_service" || link.KeyValue != "nextcloud/nextcloud-db" || link.Ignored || link.ServiceID != "" {
		t.Fatalf("non rattaché: %+v", link)
	}
	if got := callAPI(server.Server, http.MethodPost, "/api/directory/ignored", ignoredRequest{MachineID: remoteID, Kind: "compose_project", Value: "nextcloud"}); got.Code != http.StatusNoContent {
		t.Fatalf("ignorer: %d", got.Code)
	}
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/containers/"+nextcloudDBContainerID(), nil), &undeclared)
	if !undeclared.Directory.Ignored {
		t.Fatalf("ignoré: %+v", undeclared.Directory)
	}

	// Les listes ne portent pas le champ : leurs réponses figées n'ont pas bougé.
	if got := callAPI(server.Server, http.MethodGet, "/api/containers", nil); strings.Contains(got.Body.String(), `"directory"`) {
		t.Fatalf("la liste des services porte l'annuaire: %s", got.Body.String())
	}
}

// Une proposition ignorée ne se lit plus : l'écran qui la montrait encore
// apprend par un 409 qu'il faut relire la liste. Ne plus l'ignorer la rend.
func TestAPI_Proposal_IgnoredAnswers409(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	server.seedContainers(t)
	server.seedGaps(t)
	path := "/api/directory/proposal?container=" + nextcloudDBContainerID()

	ignore := ignoredRequest{MachineID: remoteID, Kind: "compose_project", Value: "nextcloud"}
	if got := callAPI(server.Server, http.MethodPost, "/api/directory/ignored", ignore); got.Code != http.StatusNoContent {
		t.Fatalf("ignorer: %d %s", got.Code, got.Body.String())
	}
	got := callAPI(server.Server, http.MethodGet, path, nil)
	if got.Code != http.StatusConflict || errorCode(t, got) != "directory.container_ignored" {
		t.Fatalf("proposition ignorée: %d %s", got.Code, got.Body.String())
	}

	query := "/api/directory/ignored?machine=" + remoteID + "&kind=compose_project&value=nextcloud"
	if got := callAPI(server.Server, http.MethodDelete, query, nil); got.Code != http.StatusNoContent {
		t.Fatalf("ne plus ignorer: %d %s", got.Code, got.Body.String())
	}
	if got := callAPI(server.Server, http.MethodGet, path, nil); got.Code != http.StatusOK {
		t.Fatalf("proposition rendue: %d %s", got.Code, got.Body.String())
	}
}

// Accepter, c'est POST /api/directory/services avec le corps de la
// proposition : le chemin unique de la création. Un second clic reçoit un
// conflit, jamais un doublon, et la proposition relue dit que c'est fait.
func TestAPI_Proposal_AcceptedTwiceAnswers409AndMakesOneService(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	server.seedContainers(t)
	path := "/api/directory/proposal?container=" + nextcloudContainerID()

	var proposal proposalResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, path, nil), &proposal)
	first := callAPI(server.Server, http.MethodPost, "/api/directory/services", proposal.Definition)
	if first.Code != http.StatusCreated {
		t.Fatalf("accepter: %d %s", first.Code, first.Body.String())
	}
	var created declaredServiceJSON
	decodeAPI(t, first, &created)
	if created.DeclaredBy != "operator" {
		t.Fatalf("déclaré par %q", created.DeclaredBy)
	}
	second := callAPI(server.Server, http.MethodPost, "/api/directory/services", proposal.Definition)
	if second.Code != http.StatusConflict || errorCode(t, second) != "directory.name_taken" {
		t.Fatalf("second clic: %d %s", second.Code, second.Body.String())
	}
	var services declaredServicesResponse
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/directory/services", nil), &services)
	if len(services.Services) != 1 {
		t.Fatalf("un seul service: %+v", services.Services)
	}
	got := callAPI(server.Server, http.MethodGet, path, nil)
	if got.Code != http.StatusConflict || errorCode(t, got) != "directory.container_declared" {
		t.Fatalf("proposition relue: %d %s", got.Code, got.Body.String())
	}
}

// Supprimer un service accepté le rend à la liste des propositions ;
// ?ignore=true le supprime et dit voulus ses conteneurs, en un geste.
func TestAPI_DeleteService_BackToAProposalOrIgnoredWithIt(t *testing.T) {
	server := newTestServer(t)
	server.enroll(t, "vps-paris-1", remoteID)
	server.seedContainers(t)
	path := "/api/directory/proposal?container=" + nextcloudContainerID()
	accept := func() string {
		t.Helper()
		var proposal proposalResponse
		decodeAPI(t, callAPI(server.Server, http.MethodGet, path, nil), &proposal)
		var created declaredServiceJSON
		decodeAPI(t, callAPI(server.Server, http.MethodPost, "/api/directory/services", proposal.Definition), &created)
		return created.ID
	}
	// Le groupe du service Compose accepté ; le service voisin du projet reste
	// proposé quoi qu'il arrive.
	undeclared := func() []undeclaredJSON {
		t.Helper()
		var gaps gapsResponse
		decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/directory/gaps?machine="+remoteID, nil), &gaps)
		var group []undeclaredJSON
		for _, found := range gaps.Undeclared {
			if found.ComposeService == "nextcloud" {
				group = append(group, found)
			} else if found.Ignored {
				t.Fatalf("seul le service supprimé est ignoré : %+v", found)
			}
		}
		return group
	}

	if got := callAPI(server.Server, http.MethodDelete, "/api/directory/services/"+accept(), nil); got.Code != http.StatusNoContent {
		t.Fatalf("supprimer: %d %s", got.Code, got.Body.String())
	}
	if groups := undeclared(); len(groups) != 1 || groups[0].Ignored || !groups[0].Adoptable {
		t.Fatalf("redevenu proposition: %+v", groups)
	}

	if got := callAPI(server.Server, http.MethodDelete, "/api/directory/services/"+accept()+"?ignore=true", nil); got.Code != http.StatusNoContent {
		t.Fatalf("supprimer et ignorer: %d %s", got.Code, got.Body.String())
	}
	if groups := undeclared(); len(groups) != 1 || !groups[0].Ignored {
		t.Fatalf("ignoré avec lui: %+v", groups)
	}
	if got := callAPI(server.Server, http.MethodDelete, "/api/directory/services/svc_inconnu?ignore=true", nil); got.Code != http.StatusNotFound {
		t.Fatalf("service inconnu: %d", got.Code)
	}
}
