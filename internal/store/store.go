package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"

	// Pilote SQLite sans cgo : un seul binaire, CGO_ENABLED=0.
	_ "modernc.org/sqlite"
)

// DatabaseFileName est le nom de la base dans le répertoire d'état.
const DatabaseFileName = "opencloud.db"

const (
	backupDirName    = "backups"
	backupNamePrefix = "opencloud-"
	backupNameSuffix = ".db"
	databaseFileMode = 0o600
	backupDirMode    = 0o700

	// Ce qu'on garde de sauvegardes avant migration. Une par migration en
	// attente, et rien ne les retirait : sur une boucle de redémarrage, le
	// disque se remplissait.
	keptMigrationBackups = 3
)

// ErrNotFound : la ligne demandée n'existe pas.
var ErrNotFound = errors.New("not found")

// Store est l'accès à la base. Un seul par processus.
type Store struct {
	db     *sql.DB
	root   *os.Root
	logger *slog.Logger
}

// Open ouvre (ou crée) la base dans le répertoire d'état et la met à niveau.
// migrations contient les fichiers NNN_sujet.sql ; en production c'est
// migrations.Files, les tests passent le leur.
func Open(ctx context.Context, root *os.Root, migrations fs.FS, logger *slog.Logger) (*Store, error) {
	// Un verrou resté là arrête tout avant la base : on ne démarre pas, on ne
	// sauvegarde pas, on ne migre pas.
	if err := refuseIfMigrationLocked(root); err != nil {
		return nil, err
	}

	databaseExisted, err := fileExists(root, DatabaseFileName)
	if err != nil {
		return nil, err
	}

	db, err := openDatabase(root)
	if err != nil {
		return nil, err
	}

	store := &Store{db: db, root: root, logger: logger}
	if err := store.migrate(ctx, migrations, databaseExisted); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func openDatabase(root *os.Root) (*sql.DB, error) {
	// SQLite veut un chemin, pas un descripteur : c'est le seul accès à l'état
	// qui sort de l'os.Root. Le dossier est celui du root (state_dir, validé),
	// le nom de fichier une constante.
	databasePath := filepath.Join(root.Name(), DatabaseFileName)

	// WAL : lecteurs et écrivain ne se bloquent pas. busy_timeout : une écriture
	// concurrente attend au lieu d'échouer. foreign_keys : SQLite ne l'active
	// pas seul.
	query := url.Values{}
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "foreign_keys(1)")
	query.Set("_time_format", "sqlite")
	dsn := "file:" + databasePath + "?" + query.Encode()

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := checkDatabase(db, root); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func checkDatabase(db *sql.DB, root *os.Root) error {
	if err := db.Ping(); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	// Un fichier de base ne se lit pas par un tiers, même si le dossier le laissait.
	if err := root.Chmod(DatabaseFileName, databaseFileMode); err != nil {
		return fmt.Errorf("protect database file: %w", err)
	}
	return nil
}

// Close ferme la base. Après Close, le Store ne sert plus.
func (s *Store) Close() error {
	return s.db.Close()
}

// Ping vérifie que la base répond.
func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func fileExists(root *os.Root, name string) (bool, error) {
	_, err := root.Stat(name)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("stat %s: %w", name, err)
}
