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
	"github.com/ldesfontaine/opencloud/internal/refusal"
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
	if err := refuseUnknownSchema(available, applied); err != nil {
		return err
	}

	var pending []migration
	for _, candidate := range available {
		if _, done := applied[candidate.version]; !done {
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
		removed, err := s.purgeOldBackups()
		if err != nil {
			return err
		}
		s.logger.Info("database backed up before migration",
			"backup", backupName, "kept", keptMigrationBackups, "removed", removed)
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

// appliedVersions rend les migrations déjà notées, numéro vers nom de fichier.
func (s *Store) appliedVersions(ctx context.Context) (map[int]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT version, name FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("list applied migrations: %w", err)
	}
	defer rows.Close()

	applied := map[int]string{}
	for rows.Next() {
		var version int
		var name string
		if err := rows.Scan(&version, &name); err != nil {
			return nil, fmt.Errorf("scan applied migration: %w", err)
		}
		applied[version] = name
	}
	return applied, rows.Err()
}

// refuseUnknownSchema arrête le démarrage quand la base porte une migration
// que ce binaire ne connaît pas : elle a été migrée par une version plus
// récente. Un .prev remis sans restaurer la sauvegarde tournerait alors sur un
// schéma plus neuf que son code, en silence.
func refuseUnknownSchema(available []migration, applied map[int]string) error {
	known := map[int]bool{}
	highestKnown := 0
	for _, candidate := range available {
		known[candidate.version] = true
		if candidate.version > highestKnown {
			highestKnown = candidate.version
		}
	}

	// La plus basse des inconnues : c'est celle dont la sauvegarde porte le nom.
	firstUnknown := 0
	for version := range applied {
		if known[version] {
			continue
		}
		if firstUnknown == 0 || version < firstUnknown {
			firstUnknown = version
		}
	}
	if firstUnknown == 0 {
		return nil
	}

	return refusal.Refusal{
		Cause: fmt.Sprintf("la base a déjà la migration %s, que ce binaire ne connaît pas (il s'arrête à la %03d) : elle a été migrée par une version plus récente d'openCloud",
			applied[firstUnknown], highestKnown),
		Remedy: fmt.Sprintf("restaurer la sauvegarde %s/%s prise avant cette migration, ou remettre le binaire plus récent",
			backupDirName, migrationBackupName("<horodatage>", firstUnknown)),
	}
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
	backupName := backupDirName + "/" + migrationBackupName(stamp, nextVersion)
	temporaryName := backupName + ".tmp"
	// VACUUM INTO veut un chemin, comme l'ouverture de la base : même sortie
	// de l'os.Root, même raison, même nom constant.
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

// migrationBackupName : l'horodatage d'abord, donc l'ordre alphabétique des
// noms est l'ordre du temps ; la version ensuite, celle qui allait être
// appliquée.
func migrationBackupName(stamp string, nextVersion int) string {
	return fmt.Sprintf("%s%s-before-%03d%s", backupNamePrefix, stamp, nextVersion, backupNameSuffix)
}

// purgeOldBackups ne garde que les keptMigrationBackups plus récentes, et ne
// tourne qu'après une sauvegarde réussie : on ne retire jamais l'avant-dernière
// avant d'avoir la dernière.
func (s *Store) purgeOldBackups() (int, error) {
	entries, err := fs.ReadDir(s.root.FS(), backupDirName)
	if err != nil {
		return 0, fmt.Errorf("list backups: %w", err)
	}

	var names []string
	for _, entry := range entries {
		if entry.IsDir() || !isMigrationBackup(entry.Name()) {
			continue
		}
		names = append(names, entry.Name())
	}
	if len(names) <= keptMigrationBackups {
		return 0, nil
	}

	sort.Strings(names)
	removed := 0
	for _, name := range names[:len(names)-keptMigrationBackups] {
		if err := s.root.Remove(backupDirName + "/" + name); err != nil {
			return removed, fmt.Errorf("remove old backup %s: %w", name, err)
		}
		removed++
	}
	return removed, nil
}

// Ce qui reste dans backups/ sans être une sauvegarde de migration — un .tmp
// laissé par une coupure — ne compte pas et ne se purge pas ici.
func isMigrationBackup(name string) bool {
	return strings.HasPrefix(name, backupNamePrefix) && strings.HasSuffix(name, backupNameSuffix)
}
