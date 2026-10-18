package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/migrations"
)

// foreignKeysOn dit si la connexion qui répond a les clés étrangères en marche.
func foreignKeysOn(t *testing.T, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) bool {
	t.Helper()
	var on int
	if err := query.QueryRowContext(context.Background(), `PRAGMA foreign_keys`).Scan(&on); err != nil {
		t.Fatal(err)
	}
	return on == 1
}

// Une migration se joue hors clés étrangères : reconstruire un parent ne vide
// pas ses enfants par cascade. Les clés sont vérifiées avant le COMMIT : une
// ligne pendante annule tout. Et la connexion revient au pool avec ses clés.
func TestMigrate_RunsWithoutForeignKeysThenRefusesAViolation(t *testing.T) {
	ctx := context.Background()
	db := openAtVersion(t, 0)
	// Une seule connexion : celle de la migration est celle qu'on interroge après.
	db.sql.SetMaxOpenConns(1)
	create := `
		CREATE TABLE parent (id INTEGER PRIMARY KEY);
		CREATE TABLE child (id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES parent (id) ON DELETE CASCADE);`
	if err := db.applyContent(ctx, "900_create.sql", 900, create, nil, nil); err != nil {
		t.Fatal(err)
	}
	exec(t, db, `INSERT INTO parent (id) VALUES (1)`)
	exec(t, db, `INSERT INTO child (id, parent_id) VALUES (1, 1)`)

	rebuild := `
		CREATE TABLE parent_new (id INTEGER PRIMARY KEY, label TEXT NOT NULL DEFAULT '');
		INSERT INTO parent_new (id) SELECT id FROM parent;
		DROP TABLE parent;
		ALTER TABLE parent_new RENAME TO parent;`
	if err := db.applyContent(ctx, "901_rebuild.sql", 901, rebuild, nil, nil); err != nil {
		t.Fatalf("reconstruire le parent: %v", err)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM child`); got != 1 {
		t.Fatalf("le DROP du parent a vidé l'enfant par cascade : %d ligne(s)", got)
	}
	if !foreignKeysOn(t, db.sql) {
		t.Fatal("les clés étrangères doivent être remises avant de rendre la connexion")
	}

	dangling := `INSERT INTO child (id, parent_id) VALUES (2, 99);`
	err := db.applyContent(ctx, "902_dangling.sql", 902, dangling, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "foreign key violated") || !strings.Contains(err.Error(), "child") {
		t.Fatalf("attendu un refus qui nomme la table child, obtenu %v", err)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM child`); got != 1 {
		t.Fatalf("la ligne pendante est restée : %d ligne(s)", got)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM schema_migrations WHERE version = 902`); got != 0 {
		t.Fatal("une migration refusée ne se note pas")
	}
	if !foreignKeysOn(t, db.sql) {
		t.Fatal("les clés étrangères doivent être remises après un refus aussi")
	}
	if _, err := db.sql.Exec(`INSERT INTO child (id, parent_id) VALUES (3, 99)`); err == nil {
		t.Fatal("la connexion rendue au pool doit refuser une clé pendante")
	}
}

// Étape Go, SQL, vérification : dans cet ordre, dans une seule transaction.
// Une erreur à l'un des trois défait tout le reste.
func TestMigrate_StepSQLAndCheckShareOneTransaction(t *testing.T) {
	ctx := context.Background()
	db := openAtVersion(t, 0)
	db.sql.SetMaxOpenConns(1)
	var order []string
	step := func(ctx context.Context, tx *sql.Tx) error {
		order = append(order, "step")
		_, err := tx.ExecContext(ctx, `CREATE TABLE from_step (id INTEGER)`)
		return err
	}
	check := func(ctx context.Context, tx *sql.Tx) error {
		order = append(order, "check")
		var seen int
		// La vérification voit ce que l'étape et le SQL ont écrit.
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM from_step`).Scan(&seen); err != nil {
			return err
		}
		if seen != 1 {
			return fmt.Errorf("check saw %d rows", seen)
		}
		return nil
	}
	content := `INSERT INTO from_step (id) VALUES (1);`

	if err := db.applyContent(ctx, "910_all.sql", 910, content, step, check); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(order, []string{"step", "check"}) {
		t.Fatalf("ordre = %v", order)
	}

	failing := errors.New("check refuses")
	stepOnly := func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TABLE rolled_back (id INTEGER)`)
		return err
	}
	refuse := func(context.Context, *sql.Tx) error { return failing }
	err := db.applyContent(ctx, "911_refused.sql", 911, `INSERT INTO from_step (id) VALUES (2);`, stepOnly, refuse)
	if !errors.Is(err, failing) {
		t.Fatalf("attendu l'erreur de la vérification, obtenu %v", err)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM from_step`); got != 1 {
		t.Fatalf("le SQL d'une migration refusée est resté : %d ligne(s)", got)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE name = 'rolled_back'`); got != 0 {
		t.Fatal("la table de l'étape d'une migration refusée est restée")
	}
	if !foreignKeysOn(t, db.sql) {
		t.Fatal("les clés étrangères doivent être remises après une annulation")
	}

	badSQL := db.applyContent(ctx, "912_bad.sql", 912, `INSERT INTO nowhere VALUES (1);`, stepOnly, nil)
	if badSQL == nil || !strings.Contains(badSQL.Error(), "912_bad.sql") {
		t.Fatalf("une erreur SQL nomme la migration: %v", badSQL)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE name = 'rolled_back'`); got != 0 {
		t.Fatal("la table de l'étape d'un SQL en erreur est restée")
	}
}

// Après Open, chaque connexion du pool a les clés étrangères : seule celle de
// la migration les quitte, le temps d'une transaction.
func TestOpen_LeavesForeignKeysOn(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	var held []*sql.Conn
	for range 4 {
		conn, err := db.sql.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, conn)
		if !foreignKeysOn(t, conn) {
			t.Fatal("une connexion du pool n'a pas les clés étrangères")
		}
	}
	for _, conn := range held {
		conn.Close()
	}
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO probes (id, name, kind, target, machine_id, status, interval_seconds, timeout_seconds,
			failure_threshold, recovery_threshold, created_at)
		VALUES ('p', 'x', 'http', 'https://a.fr/', 'inconnue', 'new', 60, 5, 3, 2, 1)`)
	if err == nil {
		t.Fatal("une clé étrangère pendante doit être refusée à l'écriture")
	}
}

// legacyOpenAt rejoue les migrations d'avant ce changement, comme l'ancien
// runner : une transaction ordinaire par fichier, clés étrangères en marche.
// Une base de production venue d'une version précédente a ce schéma.
func legacyOpenAt(t *testing.T, version int) *DB {
	t.Helper()
	ctx := context.Background()
	db := openAtVersion(t, 0)
	names, err := migrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		number := migrationVersion(name)
		if number > version {
			continue
		}
		content, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := db.sql.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, string(content)); err != nil {
			tx.Rollback()
			t.Fatalf("migration %s : %v", name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, number, time.Now().Unix()); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// schemaDump rend tout ce que le schéma dit, sans l'ordre de création : les
// objets avec leur texte, les clés étrangères de chaque table, et le nombre
// de lignes que les migrations ont semées.
func schemaDump(t *testing.T, db *DB) []string {
	t.Helper()
	var lines []string
	rows, err := db.sql.Query(`SELECT type, name, tbl_name, COALESCE(sql, '') FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' ORDER BY type, name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var kind, name, table, body string
		if err := rows.Scan(&kind, &name, &table, &body); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, kind+" "+name+" ("+table+") "+strings.Join(strings.Fields(body), " "))
		if kind == "table" {
			tables = append(tables, name)
		}
	}
	rows.Close()
	for _, table := range tables {
		if table == "schema_migrations" {
			continue
		}
		fks, err := db.sql.Query(`SELECT "from", "table", "to", on_update, on_delete FROM pragma_foreign_key_list(?) ORDER BY id, seq`, table)
		if err != nil {
			t.Fatal(err)
		}
		for fks.Next() {
			var from, parent, to, onUpdate, onDelete string
			if err := fks.Scan(&from, &parent, &to, &onUpdate, &onDelete); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, fmt.Sprintf("fk %s.%s -> %s.%s update=%s delete=%s", table, from, parent, to, onUpdate, onDelete))
		}
		fks.Close()
		lines = append(lines, fmt.Sprintf("rows %s %d", table, count(t, db, `SELECT COUNT(*) FROM "`+table+`"`)))
	}
	return lines
}

// Rejouer toutes les migrations hors clés étrangères doit donner le même
// schéma que l'ancien runner : une base neuve et une base montée de la 023 par
// la 024 sont indiscernables. Sinon, le changement de runner (qui vaut pour
// toutes les migrations) aurait dérivé en silence.
func TestMigrate_FreshAndUpgradedSchemasMatch(t *testing.T) {
	fresh := openTestDB(t)
	upgraded := legacyOpenAt(t, 23)
	if err := upgraded.migrate(context.Background()); err != nil {
		t.Fatalf("monter la base de la 023 à la dernière: %v", err)
	}

	want, got := schemaDump(t, fresh), schemaDump(t, upgraded)

	if !slices.Equal(want, got) {
		for _, line := range want {
			if !slices.Contains(got, line) {
				t.Errorf("base neuve seulement : %s", line)
			}
		}
		for _, line := range got {
			if !slices.Contains(want, line) {
				t.Errorf("base montée seulement : %s", line)
			}
		}
		t.Fatalf("les deux schémas diffèrent (%d et %d lignes)", len(want), len(got))
	}
	if len(want) < 100 {
		t.Fatalf("le schéma comparé semble vide : %d lignes", len(want))
	}
	versionsFresh, _ := fresh.AppliedVersions(context.Background())
	versionsUpgraded, _ := upgraded.AppliedVersions(context.Background())
	if !slices.Equal(versionsFresh, versionsUpgraded) {
		t.Fatalf("versions notées : %v et %v", versionsFresh, versionsUpgraded)
	}
}
