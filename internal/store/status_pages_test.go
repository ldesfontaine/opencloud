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

// La 034 range la page unique d'avant dans une page « statut » : son titre,
// son annonce et sa langue quittent settings, tous les composants la
// rejoignent, et le reste de la ligne settings est gardé.
func TestMigration034_TheSinglePageBecomesAPage(t *testing.T) {
	db := openAtVersion(t, 33)
	exec(t, db, `INSERT INTO settings (id, language, status_title, status_announcement, status_language, disk_attention_percent, mail_from, updated_at)
		VALUES (1, 'en', 'Statut de la maison', 'Coupure samedi.', 'fr', 70, 'oc@example.org', 1)`)
	exec(t, db, `INSERT INTO machines (id, name, kind, public_key, enrolled_at, created_at) VALUES (?, 'vps-1', 'remote', zeroblob(32), 1, 1)`, m1)
	exec(t, db, `INSERT INTO status_components (id, name, position, created_at) VALUES ('cmp1', 'Boutique', 2, 1), ('cmp2', 'Nuage', 1, 1)`)
	exec(t, db, `INSERT INTO status_component_members (component_id, machine_id) VALUES ('cmp1', ?)`, m1)
	exec(t, db, `INSERT INTO incidents (id, title, impact, status, created_at, updated_at) VALUES ('i1', 'Panne', 'down', 'investigating', 1, 1)`)
	exec(t, db, `INSERT INTO incident_components (incident_id, component_id) VALUES ('i1', 'cmp1')`)

	migrateToTheEnd(t, db)

	page := text(t, db, `SELECT id || '|' || slug || '|' || title || '|' || announcement || '|' || language || '|' || COALESCE(project_id, 'none') FROM status_pages`)
	if page != "page_statut|statut|Statut de la maison|Coupure samedi.|fr|none" {
		t.Fatalf("page = %q", page)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM status_components WHERE page_id = 'page_statut'`); got != 2 {
		t.Fatalf("%d composants sur la page, attendu 2", got)
	}
	if got := text(t, db, `SELECT name || ':' || position FROM status_components WHERE id = 'cmp1'`); got != "Boutique:2" {
		t.Fatalf("composant = %q", got)
	}
	if count(t, db, `SELECT COUNT(*) FROM status_component_members`) != 1 || count(t, db, `SELECT COUNT(*) FROM incident_components`) != 1 {
		t.Fatal("les membres et les liens d'incident restent")
	}
	if got := count(t, db, `SELECT COUNT(*) FROM pragma_table_info('settings') WHERE name LIKE 'status_%'`); got != 0 {
		t.Fatalf("settings garde %d colonnes status_*", got)
	}
	if got := text(t, db, `SELECT language || '|' || disk_attention_percent || '|' || mail_from FROM settings WHERE id = 1`); got != "en|70|oc@example.org" {
		t.Fatalf("settings = %q", got)
	}
	// La page emporte ses composants, et la clé étrangère le tient.
	exec(t, db, `DELETE FROM status_pages WHERE id = 'page_statut'`)
	if got := count(t, db, `SELECT COUNT(*) FROM status_components`); got != 0 {
		t.Fatalf("%d composants survivent à leur page", got)
	}
	if _, err := db.sql.Exec(`INSERT INTO status_components (id, page_id, name, created_at) VALUES ('x', 'nope', 'X', 1)`); err == nil {
		t.Fatal("un composant sans page existante doit être refusé")
	}
}

// Une page qui ne montrait rien ne se reprend pas : les pages se créent à la
// main.
func TestMigration034_AnEmptyPageIsNotKept(t *testing.T) {
	db := openAtVersion(t, 33)
	exec(t, db, `INSERT INTO settings (id, language, status_language, updated_at) VALUES (1, 'fr', 'en', 1)`)
	migrateToTheEnd(t, db)
	if got := count(t, db, `SELECT COUNT(*) FROM status_pages`); got != 0 {
		t.Fatalf("%d pages, attendu 0", got)
	}
	if got := text(t, db, `SELECT language FROM settings WHERE id = 1`); got != "fr" {
		t.Fatalf("langue = %q", got)
	}
}

// Un titre seul suffit à garder la page, même sans ligne de composant.
func TestMigration034_ATitleAloneKeepsThePage(t *testing.T) {
	db := openAtVersion(t, 33)
	exec(t, db, `INSERT INTO settings (id, status_title, updated_at) VALUES (1, 'Maison', 1)`)
	migrateToTheEnd(t, db)
	if got := text(t, db, `SELECT slug || '|' || title || '|' || language FROM status_pages`); got != "statut|Maison|" {
		t.Fatalf("page = %q", got)
	}
}
