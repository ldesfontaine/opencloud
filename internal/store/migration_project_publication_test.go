package store

import (
	"context"
	"testing"
)

// La 027 sur une base qui a déjà des pages : une page rangée dans un projet le
// publie sous son adresse, une page sans projet devient un projet nommé par son
// adresse (suffixé quand le nom est pris), les composants suivent leur projet
// avec leurs membres, et un projet sans page reste non publié.
func TestMigration027_FoldsPagesIntoProjects(t *testing.T) {
	ctx := context.Background()
	db := openEmpty(t)
	names, err := migrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if version := migrationVersion(name); version < 27 {
			if err := db.apply(ctx, name, version); err != nil {
				t.Fatal(err)
			}
		}
	}
	exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES ('m1', 'vps-1', 1)`)
	exec(t, db, `INSERT INTO projects (id, name, title, created_at, updated_at) VALUES
		('prj_client', 'client', 'Client A', 1, 1),
		('prj_vitrine', 'vitrine', '', 1, 1),
		('prj_alone', 'alone', 'Seul', 1, 1)`)
	exec(t, db, `INSERT INTO status_pages (id, project_id, slug, title, announcement, language, created_at, updated_at) VALUES
		('p_client', 'prj_client', 'client-a', 'Statut du client', 'Coupure samedi.', 'en', 10, 11),
		('p_vitrine', 'prj_vitrine', 'vitrine', 'Vitrine publique', '', '', 20, 21),
		('p_free', NULL, 'maison', 'Statut de la maison', '', 'fr', 30, 31),
		('p_taken', NULL, 'alone', '', '', '', 40, 41)`)
	exec(t, db, `INSERT INTO status_components (id, page_id, name, position, created_at) VALUES
		('c_site', 'p_client', 'Site', 2, 5), ('c_mail', 'p_client', 'Mail', 1, 5), ('c_home', 'p_free', 'Maison', 0, 5)`)
	exec(t, db, `INSERT INTO status_component_members (component_id, machine_id) VALUES ('c_site', 'm1'), ('c_home', 'm1')`)

	if err := db.apply(ctx, "027_project_publication.sql", 27); err != nil {
		t.Fatal(err)
	}

	if tables := count(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'status_pages'`); tables != 0 {
		t.Fatal("status_pages doit disparaître")
	}
	// Un projet avec page : publié sous l'adresse de la page, son titre gardé.
	if got := text(t, db, `SELECT slug || '|' || announcement || '|' || language || '|' || published_at || '|' || title FROM projects WHERE id = 'prj_client'`); got != "client-a|Coupure samedi.|en|10|Client A" {
		t.Errorf("projet publié : %q", got)
	}
	// Sans titre, il reprend celui de la page ; avec un titre, le sien l'emporte.
	if got := text(t, db, `SELECT title FROM projects WHERE id = 'prj_vitrine'`); got != "Vitrine publique" {
		t.Errorf("titre repris de la page : %q", got)
	}
	// Un projet sans page n'est pas publié.
	if got := text(t, db, `SELECT COALESCE(slug, 'non publié') || '|' || announcement || '|' || COALESCE(published_at, 'aucune') FROM projects WHERE id = 'prj_alone'`); got != "non publié||aucune" {
		t.Errorf("projet non publié : %q", got)
	}
	// Une page sans projet devient un projet nommé par son adresse.
	if got := text(t, db, `SELECT name || '|' || slug || '|' || title FROM projects WHERE id = 'prj_p_free'`); got != "maison|maison|Statut de la maison" {
		t.Errorf("page libre : %q", got)
	}
	// Nom déjà pris par un autre projet : « -statut ».
	if got := text(t, db, `SELECT name || '|' || slug FROM projects WHERE id = 'prj_p_taken'`); got != "alone-statut|alone" {
		t.Errorf("collision de nom : %q", got)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM projects`); got != 5 {
		t.Errorf("%d projets, 5 attendus", got)
	}
	// Les composants changent de parent, leurs membres les suivent.
	requireRowIDs(t, rowIDs(t, db, `SELECT id FROM status_components WHERE project_id = 'prj_client' ORDER BY position`), "c_mail", "c_site")
	requireRowIDs(t, rowIDs(t, db, `SELECT id FROM status_components WHERE project_id = 'prj_p_free'`), "c_home")
	if got := count(t, db, `SELECT COUNT(*) FROM status_component_members`); got != 2 {
		t.Errorf("%d membres, 2 attendus", got)
	}
	// Le slug est unique, et la suppression d'un projet emporte ses composants.
	if _, err := db.sql.Exec(`UPDATE projects SET slug = 'maison' WHERE id = 'prj_alone'`); err == nil {
		t.Error("deux projets ne se publient pas à la même adresse")
	}
	exec(t, db, `DELETE FROM projects WHERE id = 'prj_client'`)
	if got := count(t, db, `SELECT COUNT(*) FROM status_components`); got != 1 {
		t.Errorf("%d composants après la suppression, 1 attendu", got)
	}
}

// Deux pages libres dont les adresses se heurtent après le suffixe gardent
// chacune un nom : le début de l'identifiant de la page les départage.
func TestMigration027_NamesNeverCollide(t *testing.T) {
	ctx := context.Background()
	db := openEmpty(t)
	names, err := migrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if version := migrationVersion(name); version < 27 {
			if err := db.apply(ctx, name, version); err != nil {
				t.Fatal(err)
			}
		}
	}
	exec(t, db, `INSERT INTO projects (id, name, created_at, updated_at) VALUES ('prj_x', 'x', 1, 1)`)
	exec(t, db, `INSERT INTO status_pages (id, project_id, slug, created_at, updated_at) VALUES
		('aaaaaaaa11111111', NULL, 'x', 1, 1), ('bbbbbbbb22222222', NULL, 'x-statut', 1, 1)`)

	if err := db.apply(ctx, "027_project_publication.sql", 27); err != nil {
		t.Fatal(err)
	}

	if got := text(t, db, `SELECT name FROM projects WHERE id = 'prj_aaaaaaaa11111111'`); got != "x-aaaaaaaa" {
		t.Errorf("collision profonde : %q", got)
	}
	if got := text(t, db, `SELECT name FROM projects WHERE id = 'prj_bbbbbbbb22222222'`); got != "x-statut" {
		t.Errorf("l'adresse libre garde son nom : %q", got)
	}
	if distinct := count(t, db, `SELECT COUNT(DISTINCT name) FROM projects`); distinct != 3 {
		t.Errorf("%d noms distincts, 3 attendus", distinct)
	}
}
