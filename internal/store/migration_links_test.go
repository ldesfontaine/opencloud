package store

import (
	"context"
	"testing"
)

// La 030 sur une base qui a déjà une machine, un service Compose et un
// conteneur qui dépend d'un autre : rien ne change pour eux, la table des
// liens naît vide (les liens d'ordre se refont au démarrage depuis les
// conteneurs, par le composant), les listes fermées tiennent, et la
// suppression d'une machine emporte ses liens.
func TestMigration030_AddsTheLinksTableWithoutTouchingWhatExists(t *testing.T) {
	ctx := context.Background()
	db := openEmpty(t)
	names, err := migrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if version := migrationVersion(name); version < 30 {
			if err := db.apply(ctx, name, version); err != nil {
				t.Fatal(err)
			}
		}
	}
	exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES ('m_vps', 'vps-lyon', 1)`)
	exec(t, db, `INSERT INTO declared_services (id, machine_id, name, nature, origin, compose_project, declared_by, created_at, updated_at)
		VALUES ('svc_edge', 'm_vps', 'traefik', 'infra', 'compose', 'edge', 'operator', 1, 1)`)
	exec(t, db, `INSERT INTO service_units (id, service_id, name, kind, image, ports, position)
		VALUES ('unit_traefik', 'svc_edge', 'traefik', 'container', 'traefik', '[]', 0)`)

	if err := db.apply(ctx, "030_links.sql", 30); err != nil {
		t.Fatal(err)
	}

	if got := text(t, db, `SELECT s.name || '|' || u.name || '|' || s.compose_project FROM declared_services s JOIN service_units u ON u.service_id = s.id`); got != "traefik|traefik|edge" {
		t.Errorf("le service a changé : %q", got)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM links`); got != 0 {
		t.Errorf("%d lien(s) à la naissance, 0 attendu", got)
	}

	exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES ('m_bare', 'bare', 1)`)
	insert := `INSERT INTO links (id, from_machine_id, from_key, to_machine_id, to_key, nature, proof, signal, first_seen_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, 1)`
	if _, err := db.sql.Exec(insert, "lnk_a", "m_bare", "container:web", "m_vps", "container:db", "order", "declared", "depends_on"); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]any{
		"la même paire et la même nature": {"lnk_b", "m_bare", "container:web", "m_vps", "container:db", "order", "declared", "depends_on"},
		"une nature hors liste":           {"lnk_c", "m_bare", "container:web", "m_vps", "container:x", "magie", "declared", "depends_on"},
		"une preuve hors liste":           {"lnk_d", "m_bare", "container:web", "m_vps", "container:x", "order", "devinée", "depends_on"},
		"un signal hors liste":            {"lnk_e", "m_bare", "container:web", "m_vps", "container:x", "order", "declared", "telepathie"},
		"un lien vers soi-même":           {"lnk_f", "m_bare", "container:web", "m_bare", "container:web", "order", "declared", "depends_on"},
		"une machine inconnue":            {"lnk_g", "m_ghost", "container:web", "m_vps", "container:x", "order", "declared", "depends_on"},
	} {
		if _, err := db.sql.Exec(insert, args...); err == nil {
			t.Errorf("%s doit être refusé par la base", name)
		}
	}
	exec(t, db, `DELETE FROM machines WHERE id = 'm_bare'`)
	if got := count(t, db, `SELECT COUNT(*) FROM links`); got != 0 {
		t.Errorf("%d lien(s) survivent à la machine", got)
	}
}
