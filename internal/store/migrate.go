package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/fsx"
)

// Une migration embarquée : son numéro, son nom de fichier, son SQL.
type migration struct {
	version int
	name    string
	sql     string
}

// migrate applique, dans l'ordre, les migrations que la base n'a pas encore.
// Si la base existait déjà, elle est sauvegardée d'abord ; un échec de la
// sauvegarde empêche la migration (20-installation-et-mise-a-jour.md).
func (s *Store) migrate(ctx context.Context, files fs.FS, databaseExisted bool) error {
	if err := s.createMigrationsTable(ctx); err != nil {
		return err
	}

	available, err := readMigrations(files)
	if err != nil {
		return err
	}
	applied, err := s.appliedVersions(ctx)
	if err != nil {
		return err
	}

	var pending []migration
	for _, candidate := range available {
		if !applied[candidate.version] {
			pending = append(pending, candidate)
		}
	}
	if len(pending) == 0 {
		return nil
	}

	if databaseExisted {
		backupName, err := s.backupDatabase(ctx, pending[0].version)
		if err != nil {
			return fmt.Errorf("backup before migration %s: %w", pending[0].name, err)
		}
		s.logger.Info("database backed up before migration", "backup", backupName)
	}

	for _, candidate := range pending {
		if err := s.applyMigration(ctx, candidate); err != nil {
			return err
		}
		s.logger.Info("migration applied", "migration", candidate.name)
	}
	return nil
}

func (s *Store) createMigrationsTable(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			name       TEXT    NOT NULL,
			applied_at TEXT    NOT NULL
		)`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	return nil
}

// readMigrations lit les NNN_sujet.sql et les trie par numéro. Un nom hors
// format ou un numéro en double est une erreur : le binaire est mal construit.
func readMigrations(files fs.FS) ([]migration, error) {
	names, err := fs.Glob(files, "*.sql")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}

	var migrations []migration
	seen := map[int]string{}
	for _, name := range names {
		version, err := parseMigrationVersion(name)
		if err != nil {
			return nil, err
		}
		if previous, duplicate := seen[version]; duplicate {
			return nil, fmt.Errorf("migrations %s and %s share version %d", previous, name, version)
		}
		seen[version] = name

		content, err := fs.ReadFile(files, name)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", name, err)
		}
		migrations = append(migrations, migration{version: version, name: name, sql: string(content)})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})
	return migrations, nil
}

func parseMigrationVersion(name string) (int, error) {
	prefix, _, found := strings.Cut(name, "_")
	if !found {
		return 0, fmt.Errorf("migration %s: expected NNN_subject.sql", name)
	}
	version, err := strconv.Atoi(prefix)
	if err != nil || version <= 0 {
		return 0, fmt.Errorf("migration %s: expected a positive number before the underscore", name)
	}
	return version, nil
}

func (s *Store) appliedVersions(ctx context.Context) (map[int]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("list applied migrations: %w", err)
	}
	defer rows.Close()

	applied := map[int]bool{}
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("scan applied migration: %w", err)
		}
		applied[version] = true
	}
	return applied, rows.Err()
}

// applyMigration joue le SQL et note la version dans la même transaction :
// une coupure au milieu ne laisse ni schéma à moitié changé, ni version
// notée sans son schéma.
func (s *Store) applyMigration(ctx context.Context, candidate migration) error {
	transaction, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", candidate.name, err)
	}
	defer transaction.Rollback()

	if _, err := transaction.ExecContext(ctx, candidate.sql); err != nil {
		return fmt.Errorf("apply migration %s: %w", candidate.name, err)
	}
	_, err = transaction.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
		candidate.version, candidate.name, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("record migration %s: %w", candidate.name, err)
	}

	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", candidate.name, err)
	}
	return nil
}

// backupDatabase copie la base dans backups/ par VACUUM INTO : un instantané
// cohérent, WAL comprise. Le fichier est ensuite synchronisé et renommé, car
// VACUUM INTO ne garantit pas la durabilité de ce qu'il écrit.
func (s *Store) backupDatabase(ctx context.Context, nextVersion int) (string, error) {
	if err := s.root.Mkdir(backupDirName, backupDirMode); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", fmt.Errorf("create backup directory: %w", err)
	}

	stamp := time.Now().UTC().Format("20060102-150405")
	backupName := fmt.Sprintf("%s/opencloud-%s-before-%03d.db", backupDirName, stamp, nextVersion)
	temporaryName := backupName + ".tmp"
	temporaryPath := filepath.Join(s.root.Name(), temporaryName)

	renamed := false
	defer func() {
		if renamed {
			return
		}
		// Nettoyage après échec : l'erreur d'origine prime sur celle-ci.
		_ = s.root.Remove(temporaryName)
	}()

	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, temporaryPath); err != nil {
		return "", fmt.Errorf("vacuum into backup: %w", err)
	}
	// VACUUM INTO crée le fichier avec le umask du processus, pas avec le mode
	// de la base : une sauvegarde contient tout ce que la base contient.
	if err := s.root.Chmod(temporaryName, databaseFileMode); err != nil {
		return "", fmt.Errorf("protect backup file: %w", err)
	}
	if err := fsx.SyncFile(s.root, temporaryName); err != nil {
		return "", err
	}
	if err := s.root.Rename(temporaryName, backupName); err != nil {
		return "", fmt.Errorf("rename backup: %w", err)
	}
	renamed = true

	if err := fsx.SyncDirectory(s.root, backupDirName); err != nil {
		return "", err
	}
	return backupName, nil
}
