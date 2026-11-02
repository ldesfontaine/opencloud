package store

import (
	"context"
	"testing"
)

// La 031 sur une base qui porte déjà des liens (un lien d'ordre, un lien de
// l'opérateur, une route écartée) : ils survivent tels quels, le signal
// « environment » devient possible, les listes fermées et les index tiennent,
// et la suppression d'une machine emporte toujours ses liens.
func TestMigration031_WidensTheSignalsAndKeepsEveryExistingLink(t *testing.T) {
	ctx := context.Background()
	db := openEmpty(t)
	names, err := migrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if version := migrationVersion(name); version < 31 {
			if err := db.apply(ctx, name, version); err != nil {
				t.Fatal(err)
			}
		}
	}
	exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES ('m_vps', 'vps-lyon', 1)`)
	exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES ('m_bare', 'bare', 1)`)
	insert := `INSERT INTO links (id, from_machine_id, from_key, to_machine_id, to_key, nature, proof, signal, port, first_seen_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 10, 20)`
	before := [][]any{
		{"lnk_order", "m_vps", "compose:edge/web", "m_vps", "compose:edge/db", "order", "declared", "depends_on", 0},
		{"lnk_mine", "m_vps", "container:web", "m_bare", "container:api", "config", "declared", "operator", 8080},
		{"lnk_dismissed", "m_vps", "container:proxy", "m_vps", "container:blog", "route", "deduced", "operator", 0},
	}
	for _, args := range before {
		if _, err := db.sql.Exec(insert, args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.sql.Exec(insert, "lnk_early", "m_vps", "container:a", "m_vps", "container:b", "config", "deduced", "environment", 0); err == nil {
		t.Fatal("avant la 031, le signal « environment » n'existe pas")
	}

	if err := db.apply(ctx, "031_link_environment.sql", 31); err != nil {
		t.Fatal(err)
	}

	if got := count(t, db, `SELECT COUNT(*) FROM links`); got != 3 {
		t.Fatalf("%d lien(s) après la migration, 3 attendus", got)
	}
	if got := text(t, db, `SELECT from_key || '>' || to_key || '|' || nature || '|' || proof || '|' || signal || '|' || port || '|' || first_seen_at || '|' || last_seen_at FROM links WHERE id = 'lnk_mine'`); got != "container:web>container:api|config|declared|operator|8080|10|20" {
		t.Errorf("le lien de l'opérateur a changé : %q", got)
	}
	if _, err := db.sql.Exec(insert, "lnk_env", "m_vps", "compose:edge/web", "m_vps", "compose:edge/cache", "config", "deduced", "environment", 6379); err != nil {
		t.Fatalf("le signal « environment » doit passer : %v", err)
	}
	for name, args := range map[string][]any{
		"la même paire et la même nature": {"lnk_dup", "m_vps", "compose:edge/web", "m_vps", "compose:edge/db", "order", "declared", "depends_on", 0},
		"un signal hors liste":            {"lnk_sig", "m_vps", "container:a", "m_vps", "container:b", "config", "deduced", "telepathie", 0},
		"un lien vers soi-même":           {"lnk_self", "m_vps", "container:a", "m_vps", "container:a", "config", "deduced", "environment", 0},
		"un port hors bornes":             {"lnk_port", "m_vps", "container:a", "m_vps", "container:b", "config", "deduced", "environment", 70000},
		"une machine inconnue":            {"lnk_ghost", "m_ghost", "container:a", "m_vps", "container:b", "config", "deduced", "environment", 0},
	} {
		if _, err := db.sql.Exec(insert, args...); err == nil {
			t.Errorf("%s doit être refusé par la base", name)
		}
	}
	if got := count(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name IN ('links_pair', 'links_target')`); got != 2 {
		t.Errorf("%d index sur 2 remis", got)
	}
	exec(t, db, `DELETE FROM machines WHERE id = 'm_bare'`)
	if got := text(t, db, `SELECT group_concat(id) FROM (SELECT id FROM links ORDER BY id)`); got != "lnk_dismissed,lnk_env,lnk_order" {
		t.Errorf("la machine supprimée devait emporter son lien : %q", got)
	}
}
