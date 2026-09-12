package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pilote SQLite pur Go, enregistré sous le nom "sqlite"

	"github.com/ldesfontaine/opencloud/migrations"
)

const (
	fileName      = "opencloud.db"
	busyTimeoutMs = 5000
)

type DB struct {
	sql *sql.DB
}

// Open crée ou ouvre la base sous le répertoire d'état, en WAL, et la met
// au niveau du schéma embarqué. SQLite veut un chemin, pas un descripteur :
// c'est le seul accès fichier qui ne passe pas par os.Root.
func Open(ctx context.Context, stateDir *os.Root) (*DB, error) {
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
	db := &DB{sql: handle}
	if err := db.migrate(ctx); err != nil {
		_ = handle.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) Close() error {
	return db.sql.Close()
}

// migrate applique dans l'ordre les fichiers SQL non encore notés dans
// schema_migrations, chacun dans sa transaction.
func (db *DB) migrate(ctx context.Context) error {
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
		if applied[version] {
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
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", name, err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, string(content)); err != nil {
		return fmt.Errorf("apply migration %s: %w", name, err)
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
