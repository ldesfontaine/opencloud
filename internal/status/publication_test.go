package status_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/deletion"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/status"
)

// La page d'un projet se lit par son adresse : son titre est celui du projet
// (à défaut son nom), sa langue celle de l'interface quand elle n'en a pas.
// Un projet non publié n'a pas de page, un projet dépublié ou renommé non plus
// à l'ancienne adresse.
func TestPublications_ReadThePublishedProjects(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	project, err := f.directory.CreateProject(ctx, directory.ProjectDefinition{
		Name: "client-a", Title: "Client A",
		Publication: directory.PublicationDefinition{Slug: "client-a", Announcement: "Coupure samedi."},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.directory.CreateProject(ctx, directory.ProjectDefinition{Name: "brouillon"}); err != nil {
		t.Fatal(err)
	}
	bySlug, err := f.status.PublicationBySlug(ctx, "client-a")
	if err != nil {
		t.Fatal(err)
	}
	if bySlug.ProjectID != project.ID || bySlug.Title != "Client A" || bySlug.Language != lang.Default || bySlug.Announcement != "Coupure samedi." {
		t.Fatalf("by slug %+v", bySlug)
	}
	untitled, err := f.status.PublicationBySlug(ctx, "statut")
	if err != nil || untitled.Title != "statut" {
		t.Fatalf("sans titre, le nom du projet : %+v %v", untitled, err)
	}

	if _, err := f.directory.UpdateProject(ctx, project.ID, directory.ProjectDefinition{
		Name: "client-a", Title: "Client A",
		Publication: directory.PublicationDefinition{Slug: "client-a2", Language: lang.English},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.status.PublicationBySlug(ctx, "client-a"); !errors.Is(err, status.ErrNotFound) {
		t.Fatalf("l'ancienne adresse répond introuvable : %v", err)
	}
	moved, err := f.status.PublicationOf(ctx, project.ID)
	if err != nil || moved.Slug != "client-a2" || moved.Language != lang.English || moved.Announcement != "" {
		t.Fatalf("moved %+v %v", moved, err)
	}
	publications, err := f.status.Publications(ctx)
	if err != nil || len(publications) != 2 || publications[0].Slug != "client-a2" || publications[1].Slug != "statut" {
		t.Fatalf("publications %+v %v", publications, err)
	}

	if _, err := f.directory.UpdateProject(ctx, project.ID, directory.ProjectDefinition{Name: "client-a", Title: "Client A"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.status.PublicationOf(ctx, project.ID); !errors.Is(err, status.ErrNotFound) {
		t.Fatalf("un projet dépublié n'a plus de page : %v", err)
	}
	if _, err := f.status.Snapshot(ctx, project.ID); !errors.Is(err, status.ErrNotFound) {
		t.Fatalf("pas d'instantané sans page : %v", err)
	}
}

// Supprimer un projet publié emporte sa page, ses composants et leurs
// membres, et le plan le dit ; les incidents visent des objets, ils restent
// tels quels.
func TestDeleteProject_CarriesItsPageAndComponentsAndThePlanSaysSo(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	site := f.probe(t, "site", probe.OutcomeUp, nil)
	cloud := f.component(t, "Nuage", status.MemberRef{Kind: status.KindProbe, ID: site.ID}, status.MemberRef{Kind: status.KindMachine, ID: machine.LocalID})
	incident, err := f.status.OpenIncident(ctx, status.IncidentDefinition{
		Title: "Panne", Impact: status.ImpactDown, Status: status.StatusInvestigating, Targets: targetsOf(cloud),
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := f.directory.DeleteProjectPlan(ctx, f.projectID)
	if err != nil {
		t.Fatal(err)
	}
	wantRemoves := []deletion.Count{
		{Kind: deletion.KindPublications, Count: 1}, {Kind: deletion.KindComponents, Count: 1}, {Kind: deletion.KindMembers, Count: 2},
	}
	if plan.IsBlocked() || !slices.Equal(plan.Removes, wantRemoves) {
		t.Fatalf("plan %+v", plan)
	}
	if err := f.directory.DeleteProject(ctx, f.projectID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.status.GetComponent(ctx, cloud.ID); !errors.Is(err, status.ErrNotFound) {
		t.Fatalf("le composant part avec son projet : %v", err)
	}
	kept, err := f.status.GetIncident(ctx, incident.ID)
	if err != nil || len(kept.Targets) != 1 || len(kept.Components) != 0 {
		t.Fatalf("l'incident reste sur sa cible, montré sur aucune page : %+v %v", kept, err)
	}
	if listed, err := f.status.Incidents(ctx, ""); err != nil || len(listed) != 1 {
		t.Fatalf("l'administration le liste encore : %+v %v", listed, err)
	}
	if _, err := f.status.Snapshot(ctx, f.projectID); !errors.Is(err, status.ErrNotFound) {
		t.Fatalf("instantané d'un projet supprimé : %v", err)
	}
}

// Un composant naît sur un projet qui existe, et le plafond compte par projet.
func TestCreateComponent_NeedsItsProject(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.status.CreateComponent(ctx, status.ComponentDefinition{Name: "Site"}); !errors.Is(err, status.ErrProjectInvalid) {
		t.Fatalf("sans projet : %v", err)
	}
	if _, err := f.status.CreateComponent(ctx, status.ComponentDefinition{ProjectID: "nope", Name: "Site"}); !errors.Is(err, status.ErrProjectInvalid) {
		t.Fatalf("projet inconnu : %v", err)
	}
	for range status.MaxComponents {
		f.component(t, "Site")
	}
	if _, err := f.status.CreateComponent(ctx, status.ComponentDefinition{ProjectID: f.projectID, Name: "Site"}); !errors.Is(err, status.ErrTooMany) {
		t.Fatalf("plafond du projet : %v", err)
	}
	other := f.publish(t, "autre")
	if _, err := f.status.CreateComponent(ctx, status.ComponentDefinition{ProjectID: other, Name: "Site"}); err != nil {
		t.Fatalf("le plafond compte par projet : %v", err)
	}
}

// L'instantané d'un projet ne montre que ses composants, et d'un incident
// partagé, que les siens ; la liste des incidents d'un projet suit la même
// règle, sans réduire l'incident.
func TestSnapshot_IsPerProject(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	other := f.publish(t, "autre")
	site := f.probe(t, "boutique", probe.OutcomeUp, nil)
	mine := f.component(t, "Courrier", status.MemberRef{Kind: status.KindMachine, ID: machine.LocalID})
	_, err := f.status.CreateComponent(ctx, status.ComponentDefinition{ProjectID: other, Name: "Boutique", Members: []status.MemberRef{
		{Kind: status.KindMachine, ID: machine.LocalID}, {Kind: status.KindProbe, ID: site.ID},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// La machine est sur les deux projets ; la sonde, sur celui de la boutique.
	shared, err := f.status.OpenIncident(ctx, status.IncidentDefinition{
		Title: "Fournisseur", Impact: status.ImpactDown, Status: status.StatusInvestigating, Targets: targetsOf(mine),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.status.OpenIncident(ctx, status.IncidentDefinition{
		Title: "Boutique seule", Impact: status.ImpactDegraded, Status: status.StatusInvestigating, Targets: []status.MemberRef{{Kind: status.KindProbe, ID: site.ID}},
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.status.Snapshot(ctx, f.projectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Components) != 1 || snapshot.Components[0].Name != "Courrier" || snapshot.Global != status.StateDown {
		t.Fatalf("components %+v global %q", snapshot.Components, snapshot.Global)
	}
	if len(snapshot.Open) != 1 || snapshot.Open[0].ID != shared.ID || len(snapshot.Open[0].Components) != 1 || snapshot.Open[0].Components[0].Name != "Courrier" {
		t.Fatalf("open %+v", snapshot.Open)
	}
	incidents, err := f.status.Incidents(ctx, f.projectID)
	if err != nil || len(incidents) != 1 || len(incidents[0].Components) != 2 {
		t.Fatalf("incidents du projet %+v %v", incidents, err)
	}
	all, err := f.status.Incidents(ctx, "")
	if err != nil || len(all) != 2 {
		t.Fatalf("tous les incidents %+v %v", all, err)
	}
	if _, err := f.status.Incidents(ctx, "nope"); !errors.Is(err, status.ErrNotFound) {
		t.Fatalf("incidents d'un projet inconnu : %v", err)
	}
	components, err := f.status.Components(ctx, other)
	if err != nil || len(components) != 1 || components[0].ProjectID != other {
		t.Fatalf("composants de l'autre projet %+v %v", components, err)
	}
}

// Un projet publié sans composant saisi montre un composant par service, nommé
// comme lui ; rien n'est écrit, et le premier composant saisi les remplace
// tous. Le public n'en apprend aucun identifiant de service.
func TestSnapshot_AProjectWithoutComponentShowsOnePerService(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	project, err := f.directory.CreateProject(ctx, directory.ProjectDefinition{
		Name: "client-a", Title: "Client A", Publication: directory.PublicationDefinition{Slug: "client-a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	site := f.declareIn(t, "Boutique", project.ID)
	mail := f.declareIn(t, "Courrier", project.ID)

	components, err := f.status.Components(ctx, project.ID)
	if err != nil || len(components) != 2 {
		t.Fatalf("composants %+v %v", components, err)
	}
	names := []string{components[0].Name, components[1].Name}
	slices.Sort(names)
	if !slices.Equal(names, []string{"Boutique", "Courrier"}) || !components[0].Implicit || !components[1].Implicit {
		t.Fatalf("un composant implicite par service, nommé comme lui : %+v", components)
	}
	if stored, err := f.db.ListComponents(ctx, project.ID); err != nil || len(stored) != 0 {
		t.Fatalf("rien n'est écrit : %+v %v", stored, err)
	}

	snapshot, err := f.status.Snapshot(ctx, project.ID)
	if err != nil || len(snapshot.Components) != 2 {
		t.Fatalf("instantané %+v %v", snapshot, err)
	}
	for _, component := range snapshot.Components {
		if strings.Contains(component.ID, site) || strings.Contains(component.ID, mail) {
			t.Fatalf("l'identifiant public nomme un service : %q", component.ID)
		}
	}

	// Un incident sur un service touche son composant implicite.
	incident, err := f.status.OpenIncident(ctx, status.IncidentDefinition{
		Title: "Boutique en panne", Impact: status.ImpactDown, Status: status.StatusInvestigating,
		Targets: []status.MemberRef{{Kind: status.KindService, ID: site}},
	})
	if err != nil || len(incident.Components) != 1 || incident.Components[0].Name != "Boutique" {
		t.Fatalf("incident %+v %v", incident, err)
	}

	// Un premier composant saisi remplace les composants implicites.
	_, err = f.status.CreateComponent(ctx, status.ComponentDefinition{
		ProjectID: project.ID, Name: "Tout", Members: []status.MemberRef{{Kind: status.KindService, ID: site}},
	})
	if err != nil {
		t.Fatal(err)
	}
	components, err = f.status.Components(ctx, project.ID)
	if err != nil || len(components) != 1 || components[0].Name != "Tout" || components[0].Implicit {
		t.Fatalf("le composant saisi remplace les implicites : %+v %v", components, err)
	}
}

// Un projet non publié ne montre rien : ses services ne deviennent pas des
// composants tant qu'il n'a pas de page.
func TestComponents_AnUnpublishedProjectHasNoImplicitComponent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	project, err := f.directory.CreateProject(ctx, directory.ProjectDefinition{Name: "brouillon"})
	if err != nil {
		t.Fatal(err)
	}
	f.declareIn(t, "Boutique", project.ID)
	components, err := f.status.Components(ctx, project.ID)
	if err != nil || len(components) != 0 {
		t.Fatalf("composants %+v %v", components, err)
	}
}

// declareIn déclare le service d'un conteneur, rangé dans le projet.
func (f *fixture) declareIn(t *testing.T, name, projectID string) string {
	t.Helper()
	created, err := f.directory.CreateService(context.Background(), directory.ServiceDefinition{
		MachineID: machine.LocalID, Name: strings.ToLower(name), Title: name, Nature: directory.NatureApplication, Origin: directory.OriginContainer,
		Units:      []directory.UnitDefinition{{Name: strings.ToLower(name), Image: strings.ToLower(name), Version: "1", ContainerName: name}},
		ProjectIDs: []string{projectID},
	})
	if err != nil {
		t.Fatal(err)
	}
	return created.ID
}
