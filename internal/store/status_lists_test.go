package store

import (
	"context"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/status"
)

// Les membres de chaque composant reviennent à leur composant, dans l'ordre
// écrit, et un composant sans membre a une liste vide, jamais nulle : la
// liste se lit en bloc, pas un composant à la fois.
func TestListComponents_AttachesTheMembersOfEachComponent(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	declared := declaredOnLocal(t, db)
	pageID := insertTestPage(t, db, "statut")
	components := []status.Component{
		{ID: "c-site", PageID: pageID, Name: "Site", Position: 2, CreatedAt: incidentNow, Members: []status.Member{
			{Kind: status.KindService, ID: declared.ID}, {Kind: status.KindMachine, ID: machine.LocalID},
		}},
		{ID: "c-empty", PageID: pageID, Name: "Vide", Position: 1, CreatedAt: incidentNow},
		{ID: "c-host", PageID: pageID, Name: "Hôte", Position: 3, CreatedAt: incidentNow, Members: []status.Member{{Kind: status.KindMachine, ID: machine.LocalID}}},
	}
	for _, component := range components {
		if err := db.InsertComponent(ctx, component); err != nil {
			t.Fatal(err)
		}
	}

	listed, err := db.ListComponents(ctx, pageID)
	if err != nil {
		t.Fatal(err)
	}

	if len(listed) != 3 || listed[0].ID != "c-empty" || listed[1].ID != "c-site" || listed[2].ID != "c-host" {
		t.Fatalf("ordre = %+v", listed)
	}
	if listed[0].Members == nil || len(listed[0].Members) != 0 {
		t.Fatalf("sans membre, la liste doit être vide et non nulle : %#v", listed[0].Members)
	}
	site := listed[1].Members
	if len(site) != 2 || site[0] != (status.Member{Kind: status.KindService, ID: declared.ID}) || site[1] != (status.Member{Kind: status.KindMachine, ID: machine.LocalID}) {
		t.Fatalf("membres du site = %+v", site)
	}
	if len(listed[2].Members) != 1 || listed[2].Members[0].Kind != status.KindMachine {
		t.Fatalf("membres de l'hôte = %+v", listed[2].Members)
	}
	one, err := db.GetComponent(ctx, "c-site")
	if err != nil || len(one.Members) != 2 {
		t.Fatalf("GetComponent = %+v, %v", one, err)
	}
}

// Les composants et le fil de chaque incident reviennent à leur incident,
// le filtre de la liste s'applique aux parties comme aux incidents, et un
// incident sans composant a des listes vides, jamais nulles.
func TestListIncidents_AttachesThePartsOfEachIncident(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	open := seedIncident(t, db)
	resolvedAt := incidentNow.Add(-2 * time.Hour)
	old := status.Incident{
		ID: "i-old", Title: "Réglée", Impact: status.ImpactDegraded, Status: status.StatusResolved,
		CreatedAt: incidentNow.Add(-3 * time.Hour), UpdatedAt: resolvedAt, ResolvedAt: resolvedAt,
		Components: []status.ComponentRef{{ID: "c2"}, {ID: "c1"}},
		Updates: []status.Update{
			{Status: status.StatusInvestigating, Message: "Début.", CreatedAt: incidentNow.Add(-3 * time.Hour)},
			{Status: status.StatusResolved, Message: "Fin.", CreatedAt: resolvedAt},
		},
	}
	if err := db.InsertIncident(ctx, old); err != nil {
		t.Fatal(err)
	}
	bare := status.Incident{ID: "i-bare", Title: "Sans composant", Impact: status.ImpactDown, Status: status.StatusMonitoring, CreatedAt: incidentNow.Add(time.Minute), UpdatedAt: incidentNow.Add(time.Minute)}
	if err := db.InsertIncident(ctx, bare); err != nil {
		t.Fatal(err)
	}

	all, err := db.ListIncidents(ctx, time.Time{})
	if err != nil {
		t.Fatal(err)
	}

	if len(all) != 3 || all[0].ID != "i-bare" || all[1].ID != open.ID || all[2].ID != "i-old" {
		t.Fatalf("ordre = %+v", all)
	}
	if all[0].Components == nil || len(all[0].Components) != 0 || all[0].Updates == nil || len(all[0].Updates) != 0 {
		t.Fatalf("sans partie, les listes doivent être vides et non nulles : %#v", all[0])
	}
	if len(all[1].Components) != 1 || all[1].Components[0].Name != "c1" || len(all[1].Updates) != 1 {
		t.Fatalf("parties de l'incident ouvert = %+v", all[1])
	}
	// Les composants dans l'ordre de la page, le fil du plus récent au plus ancien.
	if got := all[2]; len(got.Components) != 2 || got.Components[0].ID != "c1" || got.Components[1].ID != "c2" ||
		len(got.Updates) != 2 || got.Updates[0].Message != "Fin." || got.Updates[1].Message != "Début." {
		t.Fatalf("parties de l'incident réglé = %+v", got)
	}

	// Résolu avant la fenêtre : l'incident réglé n'y est plus, les autres
	// gardent leurs parties.
	recent, err := db.ListIncidents(ctx, incidentNow.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 2 || len(recent[1].Components) != 1 || len(recent[1].Updates) != 1 {
		t.Fatalf("fenêtre = %+v", recent)
	}
}
