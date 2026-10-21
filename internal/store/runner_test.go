package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
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
	db := openEmpty(t)
	// Une seule connexion : celle de la migration est celle qu'on interroge après.
	db.sql.SetMaxOpenConns(1)
	create := `
		CREATE TABLE parent (id INTEGER PRIMARY KEY);
		CREATE TABLE child (id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES parent (id) ON DELETE CASCADE);`
	if err := db.applyContent(ctx, "900_create.sql", 900, create); err != nil {
		t.Fatal(err)
	}
	exec(t, db, `INSERT INTO parent (id) VALUES (1)`)
	exec(t, db, `INSERT INTO child (id, parent_id) VALUES (1, 1)`)

	rebuild := `
		CREATE TABLE parent_new (id INTEGER PRIMARY KEY, label TEXT NOT NULL DEFAULT '');
		INSERT INTO parent_new (id) SELECT id FROM parent;
		DROP TABLE parent;
		ALTER TABLE parent_new RENAME TO parent;`
	if err := db.applyContent(ctx, "901_rebuild.sql", 901, rebuild); err != nil {
		t.Fatalf("reconstruire le parent: %v", err)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM child`); got != 1 {
		t.Fatalf("le DROP du parent a vidé l'enfant par cascade : %d ligne(s)", got)
	}
	if !foreignKeysOn(t, db.sql) {
		t.Fatal("les clés étrangères doivent être remises avant de rendre la connexion")
	}

	dangling := `INSERT INTO child (id, parent_id) VALUES (2, 99);`
	err := db.applyContent(ctx, "902_dangling.sql", 902, dangling)
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

// Une base qui note une migration que le binaire n'embarque pas vient d'un
// binaire plus récent, ou d'avant le schéma unique : Open refuse, sans rien
// toucher, plutôt que de laisser le code tourner sur un autre schéma.
func TestOpen_RefusesADatabaseWithAnUnknownMigration(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	ctx := context.Background()
	db, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	exec(t, db, `INSERT INTO schema_migrations (version, applied_at) VALUES (34, 1)`)
	db.Close()

	_, err = Open(ctx, root)
	if !errors.Is(err, ErrUnknownSchema) || !strings.Contains(err.Error(), "034") {
		t.Fatalf("attendu ErrUnknownSchema qui nomme la 034, obtenu %v", err)
	}
}
