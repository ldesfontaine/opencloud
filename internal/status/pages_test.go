package status_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/deletion"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/status"
)

// Une page se crée, se relit par son adresse, se modifie ; sans langue, elle
// prend celle de l'interface.
func TestPage_CreateReadUpdate(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	created, err := f.status.CreatePage(ctx, status.PageDefinition{Slug: "client-a", Title: " Client A ", Announcement: "Coupure samedi."})
	if err != nil {
		t.Fatal(err)
	}
	if created.Slug != "client-a" || created.Title != "Client A" || created.Language != lang.Default || created.ProjectID != "" {
		t.Fatalf("created %+v", created)
	}
	bySlug, err := f.status.PageBySlug(ctx, "client-a")
	if err != nil || bySlug.ID != created.ID {
		t.Fatalf("by slug %+v %v", bySlug, err)
	}
	updated, err := f.status.UpdatePage(ctx, created.ID, status.PageDefinition{Slug: "client-a2", Title: "Client A", Language: lang.English})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Slug != "client-a2" || updated.Language != lang.English || updated.Announcement != "" {
		t.Fatalf("updated %+v", updated)
	}
	if _, err := f.status.PageBySlug(ctx, "client-a"); !errors.Is(err, status.ErrNotFound) {
		t.Fatalf("the old address must answer not found: %v", err)
	}
	pages, err := f.status.ListPages(ctx)
	if err != nil || len(pages) != 2 || pages[0].Slug != "client-a2" || pages[1].Slug != "statut" {
		t.Fatalf("pages %+v %v", pages, err)
	}
}

func TestPage_RefusesWhatMakesNoSense(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	cases := []struct {
		name       string
		definition status.PageDefinition
		want       error
	}{
		{"slug vide", status.PageDefinition{}, status.ErrSlugInvalid},
		{"slug en majuscules", status.PageDefinition{Slug: "Client"}, status.ErrSlugInvalid},
		{"slug qui commence par un tiret", status.PageDefinition{Slug: "-a"}, status.ErrSlugInvalid},
		{"slug avec une barre", status.PageDefinition{Slug: "a/b"}, status.ErrSlugInvalid},
		{"slug réservé", status.PageDefinition{Slug: "api"}, status.ErrSlugInvalid},
		{"slug trop long", status.PageDefinition{Slug: strings.Repeat("a", status.MaxSlugLength+1)}, status.ErrSlugInvalid},
		{"slug pris", status.PageDefinition{Slug: "statut"}, status.ErrSlugTaken},
		{"langue inconnue", status.PageDefinition{Slug: "b", Language: "de"}, status.ErrLanguageInvalid},
		{"annonce trop longue", status.PageDefinition{Slug: "b", Announcement: strings.Repeat("a", status.MaxAnnouncementLength+1)}, status.ErrAnnouncementInvalid},
		{"projet inconnu", status.PageDefinition{Slug: "b", ProjectID: "prj_nope"}, status.ErrProjectInvalid},
	}
	for _, tc := range cases {
		if _, err := f.status.CreatePage(ctx, tc.definition); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, err, tc.want)
		}
	}
}

// Une page par projet : le second refuse, et le projet ne se supprime plus
// tant que sa page vit.
func TestPage_OnePerProject_AndItHoldsTheProject(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	project, err := f.directory.CreateProject(ctx, directory.ProjectDefinition{Name: "client-a"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := f.status.CreatePage(ctx, status.PageDefinition{Slug: "client-a", ProjectID: project.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.status.CreatePage(ctx, status.PageDefinition{Slug: "client-a-bis", ProjectID: project.ID}); !errors.Is(err, status.ErrProjectTaken) {
		t.Fatalf("second page of the project: %v", err)
	}
	plan, err := f.directory.DeleteProjectPlan(ctx, project.ID)
	if err != nil || !plan.IsBlocked() || plan.Blockers[0].Kind != deletion.KindStatusPages || plan.Blockers[0].Objects[0].ID != page.ID {
		t.Fatalf("plan %+v %v", plan, err)
	}
	if err := f.directory.DeleteProject(ctx, project.ID); !errors.Is(err, directory.ErrProjectHasStatusPage) {
		t.Fatalf("delete a project that has a page: %v", err)
	}
	if err := f.status.DeletePage(ctx, page.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.directory.DeleteProject(ctx, project.ID); err != nil {
		t.Fatalf("the page gone, the project goes: %v", err)
	}
}

// Supprimer une page emporte ses composants et leurs membres, et le plan le
// dit ; les incidents visent des objets, ils restent tels quels.
func TestDeletePage_CarriesItsComponentsAndThePlanSaysSo(t *testing.T) {
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
	plan, err := f.status.DeletePagePlan(ctx, f.pageID)
	if err != nil {
		t.Fatal(err)
	}
	wantRemoves := []deletion.Count{{Kind: deletion.KindComponents, Count: 1}, {Kind: deletion.KindMembers, Count: 2}}
	if plan.IsBlocked() || !equalCounts(plan.Removes, wantRemoves) || len(plan.Detaches) != 0 {
		t.Fatalf("plan %+v", plan)
	}
	if err := f.status.DeletePage(ctx, f.pageID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.status.GetComponent(ctx, cloud.ID); !errors.Is(err, status.ErrNotFound) {
		t.Fatalf("the component must leave with its page: %v", err)
	}
	kept, err := f.status.GetIncident(ctx, incident.ID)
	if err != nil || len(kept.Targets) != 1 || len(kept.Components) != 0 {
		t.Fatalf("the incident stays on its target, shown on no page: %+v %v", kept, err)
	}
	if listed, err := f.status.Incidents(ctx, ""); err != nil || len(listed) != 1 {
		t.Fatalf("the administration still lists it: %+v %v", listed, err)
	}
	if _, err := f.status.Snapshot(ctx, f.pageID); !errors.Is(err, status.ErrNotFound) {
		t.Fatalf("snapshot of a deleted page: %v", err)
	}
}

func equalCounts(got, want []deletion.Count) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// Un composant naît sur une page qui existe, et le plafond compte par page.
func TestCreateComponent_NeedsItsPage(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.status.CreateComponent(ctx, status.ComponentDefinition{Name: "Site"}); !errors.Is(err, status.ErrPageInvalid) {
		t.Fatalf("no page: %v", err)
	}
	if _, err := f.status.CreateComponent(ctx, status.ComponentDefinition{PageID: "nope", Name: "Site"}); !errors.Is(err, status.ErrPageInvalid) {
		t.Fatalf("unknown page: %v", err)
	}
	for range status.MaxComponents {
		f.component(t, "Site")
	}
	other, err := f.status.CreatePage(ctx, status.PageDefinition{Slug: "autre"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.status.CreateComponent(ctx, status.ComponentDefinition{PageID: other.ID, Name: "Site"}); err != nil {
		t.Fatalf("the ceiling counts per page: %v", err)
	}
}

// L'instantané d'une page ne montre que ses composants, et d'un incident
// partagé, que les siens ; la liste des incidents d'une page suit la même
// règle, sans réduire l'incident.
func TestSnapshot_IsPerPage(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	other, err := f.status.CreatePage(ctx, status.PageDefinition{Slug: "autre"})
	if err != nil {
		t.Fatal(err)
	}
	site := f.probe(t, "boutique", probe.OutcomeUp, nil)
	mine := f.component(t, "Courrier", status.MemberRef{Kind: status.KindMachine, ID: machine.LocalID})
	_, err = f.status.CreateComponent(ctx, status.ComponentDefinition{PageID: other.ID, Name: "Boutique", Members: []status.MemberRef{
		{Kind: status.KindMachine, ID: machine.LocalID}, {Kind: status.KindProbe, ID: site.ID},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// La machine est sur les deux pages ; la sonde, sur celle de la boutique.
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
	snapshot, err := f.status.Snapshot(ctx, f.pageID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Components) != 1 || snapshot.Components[0].Name != "Courrier" || snapshot.Global != status.StateDown {
		t.Fatalf("components %+v global %q", snapshot.Components, snapshot.Global)
	}
	if len(snapshot.Open) != 1 || snapshot.Open[0].ID != shared.ID || len(snapshot.Open[0].Components) != 1 || snapshot.Open[0].Components[0].Name != "Courrier" {
		t.Fatalf("open %+v", snapshot.Open)
	}
	incidents, err := f.status.Incidents(ctx, f.pageID)
	if err != nil || len(incidents) != 1 || len(incidents[0].Components) != 2 {
		t.Fatalf("incidents of the page %+v %v", incidents, err)
	}
	all, err := f.status.Incidents(ctx, "")
	if err != nil || len(all) != 2 {
		t.Fatalf("all incidents %+v %v", all, err)
	}
	if _, err := f.status.Incidents(ctx, "nope"); !errors.Is(err, status.ErrNotFound) {
		t.Fatalf("incidents of an unknown page: %v", err)
	}
	components, err := f.status.Components(ctx, other.ID)
	if err != nil || len(components) != 1 || components[0].PageID != other.ID {
		t.Fatalf("components of the other page %+v %v", components, err)
	}
}
