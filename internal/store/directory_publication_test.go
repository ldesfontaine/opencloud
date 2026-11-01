package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/deletion"
	"github.com/ldesfontaine/opencloud/internal/directory"
)

// insertTestPage écrit un projet publié à cette adresse, ou rend celui qui y
// est : un composant naît sur un projet.
func insertTestPage(t *testing.T, db *DB, slug string) string {
	t.Helper()
	ctx := context.Background()
	projects, err := db.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, found := range projects {
		if found.Publication.Slug == slug {
			return found.ID
		}
	}
	project := directory.Project{
		ID: "prj_" + slug, Name: slug, CreatedAt: incidentNow, UpdatedAt: incidentNow,
		Publication: directory.Publication{Slug: slug, PublishedAt: incidentNow},
	}
	if err := db.InsertProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	return project.ID
}

// La base refuse ce que le composant refuse : un nom pris, un slug pris ;
// chaque refus garde sa clé. Un projet qui se réécrit garde son propre slug.
func TestInsertProject_RefusesTakenKeys(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	insertTestPage(t, db, "statut")
	at := time.Unix(1, 0)
	published := directory.Publication{Slug: "statut", PublishedAt: at}
	if err := db.InsertProject(ctx, directory.Project{ID: "prj_b", Name: "b", Publication: published, CreatedAt: at, UpdatedAt: at}); !errors.Is(err, directory.ErrSlugTaken) {
		t.Fatalf("slug pris : %v", err)
	}
	if err := db.InsertProject(ctx, directory.Project{ID: "prj_b", Name: "statut", CreatedAt: at, UpdatedAt: at}); !errors.Is(err, directory.ErrProjectNameTaken) {
		t.Fatalf("nom pris : %v", err)
	}
	if err := db.InsertProject(ctx, directory.Project{ID: "prj_b", Name: "b", CreatedAt: at, UpdatedAt: at}); err != nil {
		t.Fatal(err)
	}
	// Réécrire son propre projet avec son propre slug passe.
	own := directory.Project{ID: "prj_statut", Name: "statut", Title: "Statut", Publication: published, CreatedAt: at, UpdatedAt: at}
	if err := db.UpdateProject(ctx, own); err != nil {
		t.Fatal(err)
	}
	found, err := db.GetProject(ctx, "prj_statut")
	if err != nil || found.Title != "Statut" || found.Publication.Slug != "statut" || !found.Publication.PublishedAt.Equal(at) {
		t.Fatalf("projet relu %+v %v", found, err)
	}
	// Dépublier rend le slug à qui le veut.
	own.Publication = directory.Publication{}
	if err := db.UpdateProject(ctx, own); err != nil {
		t.Fatal(err)
	}
	other := directory.Project{ID: "prj_b", Name: "b", Publication: published, CreatedAt: at, UpdatedAt: at}
	if err := db.UpdateProject(ctx, other); err != nil {
		t.Fatalf("slug rendu : %v", err)
	}
}

// Le plafond de pages publiées vaut pour une publication de plus, pas pour la
// réécriture d'une page déjà publiée.
func TestInsertProject_RefusesPublicationsPastTheCeiling(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	for index := 0; index < directory.MaxPublications; index++ {
		insertTestPage(t, db, fmt.Sprintf("page-%d", index))
	}
	at := time.Unix(1, 0)
	extra := directory.Project{ID: "prj_extra", Name: "extra", Publication: directory.Publication{Slug: "extra", PublishedAt: at}, CreatedAt: at, UpdatedAt: at}
	if err := db.InsertProject(ctx, extra); !errors.Is(err, directory.ErrTooManyPublications) {
		t.Fatalf("plafond : %v", err)
	}
	extra.Publication = directory.Publication{}
	if err := db.InsertProject(ctx, extra); err != nil {
		t.Fatalf("un projet non publié passe : %v", err)
	}
	rewritten := directory.Project{ID: "prj_page-0", Name: "page-0", Title: "Zéro", Publication: directory.Publication{Slug: "page-0", PublishedAt: at}, CreatedAt: at, UpdatedAt: at}
	if err := db.UpdateProject(ctx, rewritten); err != nil {
		t.Fatalf("réécrire une page publiée passe : %v", err)
	}
}

// Un projet part avec sa page publique, ses composants et leurs membres ;
// l'incident vise des objets, il reste tel quel.
func TestDeleteProject_TakesItsPublicationAndComponents(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seedIncident(t, db)
	projectID := insertTestPage(t, db, "statut")
	plan, err := db.ProjectDeletionPlan(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if plan.IsBlocked() || len(plan.Removes) < 2 ||
		plan.Removes[0] != (deletion.Count{Kind: deletion.KindPublications, Count: 1}) ||
		plan.Removes[1] != (deletion.Count{Kind: deletion.KindComponents, Count: 2}) {
		t.Fatalf("plan %+v", plan)
	}
	if err := db.DeleteProject(ctx, projectID); err != nil {
		t.Fatal(err)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM status_components`); got != 0 {
		t.Fatalf("%d composants restent", got)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM incidents`) + count(t, db, `SELECT COUNT(*) FROM incident_targets`); got != 2 {
		t.Fatalf("l'incident et sa cible doivent rester : %d", got)
	}
	if err := db.DeleteProject(ctx, projectID); !errors.Is(err, directory.ErrProjectNotFound) {
		t.Fatalf("deux fois : %v", err)
	}
}
