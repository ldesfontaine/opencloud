package store

import (
	"context"
	"testing"
)

// La 009 refait heartbeat_runs pour admettre « missed » : les lignes gardent
// leur identifiant (l'API trie par id), l'index des exécutions ouvertes reste
// unique, une ligne « missed » est admise et une issue inconnue refusée.
func TestMigration009_KeepsTheRunsAndAdmitsMissed(t *testing.T) {
	ctx := context.Background()
	db := openEmpty(t)
	names, err := migrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if version := migrationVersion(name); version < 9 {
			if err := db.apply(ctx, name, version); err != nil {
				t.Fatal(err)
			}
		}
	}
	exec(t, db, `INSERT INTO heartbeats (id, name, token_sealed, token_lookup, status, interval_seconds, grace_seconds, created_at)
		VALUES ('hb', 'job', 's', 'l', 'on_time', 300, 60, 1)`)
	exec(t, db, `INSERT INTO heartbeat_runs (id, heartbeat_id, started_at, completed_at, duration_ms, exit_code, outcome, payload)
		VALUES (7, 'hb', 100, 160, 60000, 0, 'success', 'ok'), (9, 'hb', 200, NULL, NULL, NULL, 'in_progress', '')`)

	if err := db.apply(ctx, "009_heartbeat_missed.sql", 9); err != nil {
		t.Fatal(err)
	}

	if rows := count(t, db, `SELECT COUNT(*) FROM heartbeat_runs WHERE id IN (7, 9)`); rows != 2 {
		t.Fatalf("%d lignes gardées sur 2 : les identifiants doivent survivre", rows)
	}
	if got := text(t, db, `SELECT payload FROM heartbeat_runs WHERE id = 7`); got != "ok" {
		t.Fatalf("contenu perdu : %q", got)
	}
	exec(t, db, `INSERT INTO heartbeat_runs (heartbeat_id, completed_at, outcome) VALUES ('hb', 300, 'missed')`)
	if _, err := db.sql.Exec(`INSERT INTO heartbeat_runs (heartbeat_id, outcome) VALUES ('hb', 'in_progress')`); err == nil {
		t.Fatal("une seconde exécution ouverte est acceptée : l'index partiel a disparu")
	}
	if _, err := db.sql.Exec(`INSERT INTO heartbeat_runs (heartbeat_id, outcome) VALUES ('hb', 'oublie')`); err == nil {
		t.Fatal("une issue inconnue est acceptée")
	}

	// Les colonnes dans l'ordre de 001, les deux index à l'identique.
	if got := text(t, db, `SELECT group_concat(name, ',') FROM pragma_table_info('heartbeat_runs')`); got != "id,heartbeat_id,started_at,completed_at,duration_ms,exit_code,outcome,payload" {
		t.Fatalf("colonnes : %s", got)
	}
	indexes := text(t, db, `SELECT group_concat(sql, ' | ') FROM (SELECT sql FROM sqlite_master WHERE type = 'index' AND tbl_name = 'heartbeat_runs' ORDER BY name)`)
	if want := "CREATE INDEX heartbeat_runs_by_heartbeat ON heartbeat_runs (heartbeat_id, id DESC) | " +
		"CREATE UNIQUE INDEX heartbeat_runs_open ON heartbeat_runs (heartbeat_id) WHERE outcome = 'in_progress'"; indexes != want {
		t.Fatalf("index : %s", indexes)
	}
	// La clé étrangère est refaite avec la table : la tâche retirée emporte
	// ses exécutions, la nouvelle comprise.
	exec(t, db, `DELETE FROM heartbeats WHERE id = 'hb'`)
	if rows := count(t, db, `SELECT COUNT(*) FROM heartbeat_runs`); rows != 0 {
		t.Fatalf("%d exécutions survivent à leur tâche : la cascade est perdue", rows)
	}
}
