package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/deletion"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/status"
)

// insertTestPage écrit une page à cette adresse, ou rend celle qui y est :
// un composant naît sur une page.
func insertTestPage(t *testing.T, db *DB, slug string) string {
	t.Helper()
	ctx := context.Background()
	if found, err := db.GetPageBySlug(ctx, slug); err == nil {
		return found.ID
	}
	page := status.Page{ID: "page_" + slug, Slug: slug, CreatedAt: incidentNow, UpdatedAt: incidentNow}
	if err := db.InsertPage(ctx, page); err != nil {
		t.Fatal(err)
	}
	return page.ID
}

// La base refuse ce que le composant refuse : un slug pris, un projet
// inconnu, un projet qui a déjà sa page ; chaque refus garde sa clé.
func TestInsertPage_RefusesTakenKeys(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	insertTestPage(t, db, "statut")
	at := time.Unix(1, 0)
	if err := db.InsertPage(ctx, status.Page{ID: "p2", Slug: "statut", CreatedAt: at, UpdatedAt: at}); !errors.Is(err, status.ErrSlugTaken) {
		t.Fatalf("slug pris : %v", err)
	}
	if err := db.InsertPage(ctx, status.Page{ID: "p2", Slug: "b", ProjectID: "prj_nope", CreatedAt: at, UpdatedAt: at}); !errors.Is(err, status.ErrProjectInvalid) {
		t.Fatalf("projet inconnu : %v", err)
	}
	if err := db.InsertProject(ctx, directory.Project{ID: "prj_a", Name: "a", CreatedAt: at, UpdatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertPage(ctx, status.Page{ID: "p2", Slug: "b", ProjectID: "prj_a", CreatedAt: at, UpdatedAt: at}); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertPage(ctx, status.Page{ID: "p3", Slug: "c", ProjectID: "prj_a", CreatedAt: at, UpdatedAt: at}); !errors.Is(err, status.ErrProjectTaken) {
		t.Fatalf("projet déjà servi : %v", err)
	}
	// Réécrire sa propre page avec son propre slug et son projet passe.
	if err := db.UpdatePage(ctx, status.Page{ID: "p2", Slug: "b", ProjectID: "prj_a", Title: "B", UpdatedAt: at}); err != nil {
		t.Fatal(err)
	}
	found, err := db.GetPage(ctx, "p2")
	if err != nil || found.Title != "B" || found.ProjectID != "prj_a" {
		t.Fatalf("page relue %+v %v", found, err)
	}
	// Le filet sous le plan : la clé étrangère retient le projet.
	if _, err := db.sql.ExecContext(ctx, `DELETE FROM projects WHERE id = 'prj_a'`); err == nil {
		t.Fatal("un projet qui a sa page ne se supprime pas en brut")
	}
	if err := db.DeleteProject(ctx, "prj_a"); !errors.Is(err, directory.ErrProjectHasStatusPage) {
		t.Fatalf("suppression par le store : %v", err)
	}
}

// Une page part avec ses composants et leurs membres ; les liens d'incident
// partent avec eux, l'incident reste.
func TestDeletePage_CascadesToItsComponents(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seedIncident(t, db)
	pageID := insertTestPage(t, db, "statut")
	plan, err := db.PageDeletionPlan(ctx, pageID)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Removes) != 1 || plan.Removes[0] != (deletion.Count{Kind: deletion.KindComponents, Count: 2}) ||
		len(plan.Detaches) != 1 || plan.Detaches[0] != (deletion.Count{Kind: deletion.KindIncidentLinks, Count: 1}) {
		t.Fatalf("plan %+v", plan)
	}
	if err := db.DeletePage(ctx, pageID); err != nil {
		t.Fatal(err)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM status_components`); got != 0 {
		t.Fatalf("%d composants restent", got)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM incidents`); got != 1 {
		t.Fatalf("l'incident doit rester : %d", got)
	}
	if err := db.DeletePage(ctx, pageID); !errors.Is(err, status.ErrNotFound) {
		t.Fatalf("deux fois : %v", err)
	}
}
