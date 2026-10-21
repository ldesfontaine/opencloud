package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pilote SQLite pur Go, enregistré sous le nom "sqlite"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/reader"
	"github.com/ldesfontaine/opencloud/migrations"
)

// La base est le Reader que le serveur, la page de statut et le fichier de
// l'annuaire reçoivent : une méthode qui lui manque se voit à la compilation.
var _ reader.Reader = (*DB)(nil)

const (
	fileName      = brand.DatabaseFile
	busyTimeoutMs = 5000
)

type DB struct {
	sql *sql.DB
	// Chiffre les secrets qui entrent en base et les ouvre à la lecture ;
	// posé par SetSealer au démarrage.
	sealer Sealer
}

// Open crée ou ouvre la base sous le répertoire d'état, en WAL, et la met
// au niveau du schéma embarqué. SQLite veut un chemin, pas un descripteur :
// c'est le seul accès fichier qui ne passe pas par os.Root.
func Open(ctx context.Context, stateDir *os.Root) (*DB, error) {
	handle, err := openHandle(stateDir)
	if err != nil {
		return nil, err
	}
	db := &DB{sql: handle}
	if err := db.migrate(ctx); err != nil {
		_ = handle.Close()
		return nil, err
	}
	return db, nil
}

// openHandle ouvre la base sans y toucher : les tests s'en servent pour
// poser un schéma ancien, avant de laisser Open le mettre à niveau.
func openHandle(stateDir *os.Root) (*sql.DB, error) {
	path := filepath.Join(stateDir.Name(), fileName)
	dsn := "file:" + path + "?" + url.Values{
		"_pragma": {
			"journal_mode(WAL)",
			"busy_timeout(" + strconv.Itoa(busyTimeoutMs) + ")",
			"foreign_keys(ON)",
		},
		// Une transaction prend le verrou d'écriture dès son début : deux
		// écrivains ne se découvrent jamais au COMMIT.
		"_txlock": {"immediate"},
	}.Encode()
	handle, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	return handle, nil
}

func (db *DB) Close() error {
	return db.sql.Close()
}

// migrate met la base au niveau du schéma embarqué.
func (db *DB) migrate(ctx context.Context) error {
	return db.migrateTo(ctx, math.MaxInt)
}

// migrateTo applique dans l'ordre les fichiers SQL non encore notés dans
// schema_migrations, jusqu'à la version last comprise, chacun dans sa
// transaction. Les tests s'arrêtent avant la dernière pour y semer de
// l'existant.
func (db *DB) migrateTo(ctx context.Context, last int) error {
	if _, err := db.sql.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`,
	); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	applied, err := db.appliedVersions(ctx)
	if err != nil {
		return err
	}
	names, err := migrationFiles()
	if err != nil {
		return err
	}
	for _, name := range names {
		version := migrationVersion(name)
		if applied[version] || version > last {
			continue
		}
		if err := db.apply(ctx, name, version); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) appliedVersions(ctx context.Context) (map[int]bool, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()
	applied := map[int]bool{}
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		applied[version] = true
	}
	return applied, rows.Err()
}

func (db *DB) apply(ctx context.Context, name string, version int) error {
	content, err := migrations.Files.ReadFile(name)
	if err != nil {
		return fmt.Errorf("read migration %s: %w", name, err)
	}
	return db.applyContent(ctx, name, version, string(content), migrationSteps[version], migrationChecks[version])
}

// migrationStep est le Go d'une migration qui ne s'écrit pas en SQL sans se
// dupliquer : il passe avant le SQL de sa version, dans la même transaction.
type migrationStep func(ctx context.Context, tx *sql.Tx) error

// migrationSteps range les migrations qui demandent du Go : le
// rapprochement et le nommage ne s'écrivent pas en SQL sans se dupliquer.
// L'étape lit et écrit avec ses propres requêtes, figées sur le schéma de sa
// version, jamais avec celles du store, qui suivent le schéma courant.
var migrationSteps = map[int]migrationStep{
	24: linkContainersToDirectory,
}

// migrationChecks passent après le SQL de leur version, avant la vérification
// des clés étrangères : une migration qui reconstruit des tables y compte ce
// qu'elle devait garder.
var migrationChecks = map[int]migrationStep{
	24: verifyContainerLinks,
}

// applyContent joue une migration hors clés étrangères, puis les vérifie
// avant de valider. Reconstruire une table que d'autres référencent, selon
// la procédure de SQLite, ne se fait pas clés étrangères en marche : le
// DROP TABLE viderait par cascade l'historique des tables qui en dépendent.
// Le pragma n'a aucun effet dans une transaction : il se pose sur une
// connexion à part, avant le BEGIN, et se remet avant de la rendre au pool.
// Une clé étrangère violée après coup annule tout : la migration est faite en
// entier ou pas du tout.
func (db *DB) applyContent(ctx context.Context, name string, version int, content string, step, check migrationStep) (err error) {
	conn, err := db.sql.Conn(ctx)
	if err != nil {
		return fmt.Errorf("connect for migration %s: %w", name, err)
	}
	defer func() { err = errors.Join(err, giveBack(conn)) }()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("disable foreign keys for migration %s: %w", name, err)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", name, err)
	}
	defer tx.Rollback()
	if step != nil {
		if err := step(ctx, tx); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
	}
	if _, err := tx.ExecContext(ctx, content); err != nil {
		return fmt.Errorf("apply migration %s: %w", name, err)
	}
	if check != nil {
		if err := check(ctx, tx); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
	}
	if err := checkForeignKeys(ctx, tx); err != nil {
		return fmt.Errorf("migration %s: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, version, time.Now().Unix(),
	); err != nil {
		return fmt.Errorf("record migration %s: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", name, err)
	}
	return nil
}

// checkForeignKeys refuse une base où une clé étrangère pend : la première
// ligne rendue suffit, avec la table et la clé dans l'erreur.
func checkForeignKeys(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("check foreign keys: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return rows.Err()
	}
	var table, parent string
	var rowID sql.NullInt64
	var key int
	if err := rows.Scan(&table, &rowID, &parent, &key); err != nil {
		return fmt.Errorf("read foreign key violation: %w", err)
	}
	return fmt.Errorf("foreign key violated: table %s, row %d, references %s (key %d)", table, rowID.Int64, parent, key)
}

// giveBack remet les clés étrangères en marche avant de rendre la connexion
// au pool : une connexion sans elles ne doit jamais servir à autre chose. Si
// la remise échoue, la connexion est jetée plutôt que rendue. La transaction
// est déjà finie quand le defer s'exécute, sinon le pragma serait sans effet.
func giveBack(conn *sql.Conn) error {
	reset := context.WithoutCancel(context.Background())
	_, err := conn.ExecContext(reset, `PRAGMA foreign_keys = ON`)
	if err != nil {
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		_ = conn.Close()
		return fmt.Errorf("enable foreign keys again: %w", err)
	}
	return conn.Close()
}

// Les fichiers s'appellent NNN_sujet.sql ; le numéro fait l'ordre.
func migrationFiles() ([]string, error) {
	entries, err := fs.ReadDir(migrations.Files, ".")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func migrationVersion(name string) int {
	number, _, _ := strings.Cut(name, "_")
	version, err := strconv.Atoi(number)
	if err != nil {
		return 0
	}
	return version
}

// AppliedVersions liste les migrations jouées, pour les tests et le journal.
func (db *DB) AppliedVersions(ctx context.Context) ([]int, error) {
	applied, err := db.appliedVersions(ctx)
	if err != nil {
		return nil, err
	}
	versions := make([]int, 0, len(applied))
	for version := range applied {
		versions = append(versions, version)
	}
	sort.Ints(versions)
	return versions, nil
}
