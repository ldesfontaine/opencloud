package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/backup"
)

var _ backup.Store = (*DB)(nil)

// La clé de sauvegarde tient en une ligne, d'identifiant 1 (migration 006).
const keyringRow = 1

func (db *DB) GetBackupKeyring(ctx context.Context) (backup.Keyring, error) {
	var keyring backup.Keyring
	var createdAt int64
	err := db.sql.QueryRowContext(ctx, `
		SELECT recipient, identity_sealed, fingerprint, created_at FROM backup_keyring WHERE id = ?`, keyringRow,
	).Scan(&keyring.Recipient, &keyring.IdentitySealed, &keyring.Fingerprint, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return backup.Keyring{}, backup.ErrNotFound
	}
	if err != nil {
		return backup.Keyring{}, fmt.Errorf("get backup keyring: %w", err)
	}
	keyring.CreatedAt = time.Unix(createdAt, 0)
	return keyring, nil
}

// SaveBackupKeyring écrit l'unique paire, ou remplace celle qui existe.
func (db *DB) SaveBackupKeyring(ctx context.Context, keyring backup.Keyring) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO backup_keyring (id, recipient, identity_sealed, fingerprint, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET recipient = excluded.recipient, identity_sealed = excluded.identity_sealed,
			fingerprint = excluded.fingerprint, created_at = excluded.created_at`,
		keyringRow, keyring.Recipient, keyring.IdentitySealed, keyring.Fingerprint, keyring.CreatedAt.Unix())
	if err != nil {
		return fmt.Errorf("save backup keyring: %w", err)
	}
	return nil
}

const destinationColumns = `d.id, d.name, d.machine_id, m.name, d.path, d.created_at`

func (db *DB) ListBackupDestinations(ctx context.Context) ([]backup.Destination, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT `+destinationColumns+`
		FROM backup_destinations d JOIN machines m ON m.id = d.machine_id
		ORDER BY d.name, d.id`)
	if err != nil {
		return nil, fmt.Errorf("list backup destinations: %w", err)
	}
	return collect(rows, scanBackupDestination)
}

func (db *DB) GetBackupDestination(ctx context.Context, id string) (backup.Destination, error) {
	destination, err := scanBackupDestination(db.sql.QueryRowContext(ctx, `
		SELECT `+destinationColumns+`
		FROM backup_destinations d JOIN machines m ON m.id = d.machine_id
		WHERE d.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return backup.Destination{}, backup.ErrNotFound
	}
	if err != nil {
		return backup.Destination{}, fmt.Errorf("get backup destination: %w", err)
	}
	return destination, nil
}

func scanBackupDestination(row scanner) (backup.Destination, error) {
	var destination backup.Destination
	var createdAt int64
	err := row.Scan(&destination.ID, &destination.Name, &destination.MachineID, &destination.MachineName, &destination.Path, &createdAt)
	destination.CreatedAt = time.Unix(createdAt, 0)
	return destination, err
}

func (db *DB) InsertBackupDestination(ctx context.Context, destination backup.Destination) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO backup_destinations (id, name, machine_id, path, created_at) VALUES (?, ?, ?, ?, ?)`,
		destination.ID, destination.Name, destination.MachineID, destination.Path, destination.CreatedAt.Unix())
	if isUniqueViolation(err) {
		return backup.ErrNameTaken
	}
	if err != nil {
		return fmt.Errorf("insert backup destination: %w", err)
	}
	return nil
}

// DeleteBackupDestination refuse tant que le plan s'y envoie : la clé
// étrangère le ferait aussi (restrict), mais en erreur brute.
func (db *DB) DeleteBackupDestination(ctx context.Context, id string) error {
	return db.inTx(ctx, "delete backup destination", func(tx *sql.Tx) error {
		var users int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM backup_plans WHERE destination_id = ?`, id).Scan(&users); err != nil {
			return fmt.Errorf("count plans of destination: %w", err)
		}
		if users > 0 {
			return backup.ErrDestinationInUse
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM backup_destinations WHERE id = ?`, id)
		if err != nil {
			return fmt.Errorf("delete backup destination: %w", err)
		}
		return notFoundIfNoRow(result, backup.ErrNotFound)
	})
}

func (db *DB) CountBackupDestinations(ctx context.Context) (int, error) {
	var count int
	if err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM backup_destinations`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count backup destinations: %w", err)
	}
	return count, nil
}

// planColumns lit un plan avec le nom de sa machine source ; la jointure
// est à gauche, la base n'en a pas.
const planColumns = `p.id, p.subject, p.name, p.source_kind, p.source_machine_id, COALESCE(m.name, ''), p.producer, p.selector,
	p.destination_id, p.schedule, p.keep, p.last_attempt_at, p.last_error, p.last_run_id, p.created_at, p.updated_at`

const planSource = `FROM backup_plans p LEFT JOIN machines m ON m.id = p.source_machine_id`

// ListBackupPlans rend la base d'abord, puis les autres par sujet.
func (db *DB) ListBackupPlans(ctx context.Context) ([]backup.Plan, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT `+planColumns+` `+planSource+`
		ORDER BY p.source_kind = 'database' DESC, p.subject`)
	if err != nil {
		return nil, fmt.Errorf("list backup plans: %w", err)
	}
	return collect(rows, scanBackupPlan)
}

func (db *DB) GetBackupPlan(ctx context.Context, subject string) (backup.Plan, error) {
	plan, err := scanBackupPlan(db.sql.QueryRowContext(ctx, `
		SELECT `+planColumns+` `+planSource+`
		WHERE p.subject = ?`, subject))
	if errors.Is(err, sql.ErrNoRows) {
		return backup.Plan{}, backup.ErrNotFound
	}
	if err != nil {
		return backup.Plan{}, fmt.Errorf("get backup plan: %w", err)
	}
	return plan, nil
}

func scanBackupPlan(row scanner) (backup.Plan, error) {
	var plan backup.Plan
	var sourceMachineID, producer, selector, lastRunID sql.NullString
	var lastAttemptAt sql.NullInt64
	var createdAt, updatedAt int64
	err := row.Scan(&plan.ID, &plan.Subject, &plan.Name, &plan.SourceKind, &sourceMachineID, &plan.SourceMachineName,
		&producer, &selector, &plan.DestinationID, &plan.Schedule, &plan.Keep, &lastAttemptAt, &plan.LastError,
		&lastRunID, &createdAt, &updatedAt)
	plan.SourceMachineID, plan.Producer, plan.Selector = sourceMachineID.String, producer.String, selector.String
	plan.LastAttemptAt, plan.LastRunID = timeOf(lastAttemptAt), lastRunID.String
	plan.CreatedAt, plan.UpdatedAt = time.Unix(createdAt, 0), time.Unix(updatedAt, 0)
	return plan, err
}

// SaveBackupPlan crée le plan, ou change son libellé, sa source, sa
// destination, son nombre gardé et son rythme : sa dernière tentative reste
// ce qu'elle était.
func (db *DB) SaveBackupPlan(ctx context.Context, plan backup.Plan) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO backup_plans (id, subject, name, source_kind, source_machine_id, producer, selector, destination_id,
			schedule, keep, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (subject) DO UPDATE SET name = excluded.name, source_kind = excluded.source_kind,
			source_machine_id = excluded.source_machine_id, producer = excluded.producer, selector = excluded.selector,
			destination_id = excluded.destination_id, schedule = excluded.schedule, keep = excluded.keep,
			updated_at = excluded.updated_at`,
		plan.ID, plan.Subject, plan.Name, plan.SourceKind, nullableString(plan.SourceMachineID), nullableString(plan.Producer),
		nullableString(plan.Selector), plan.DestinationID, plan.Schedule, plan.Keep, plan.CreatedAt.Unix(), plan.UpdatedAt.Unix())
	if err != nil {
		return fmt.Errorf("save backup plan: %w", err)
	}
	return nil
}

func (db *DB) DeleteBackupPlan(ctx context.Context, subject string) error {
	result, err := db.sql.ExecContext(ctx, `DELETE FROM backup_plans WHERE subject = ?`, subject)
	if err != nil {
		return fmt.Errorf("delete backup plan: %w", err)
	}
	return notFoundIfNoRow(result, backup.ErrNotFound)
}

// NoteBackupAttempt écrit la dernière tentative du plan : son instant, la clé
// de ce qui l'a arrêtée avant l'ordre (vide si l'ordre est parti) et son
// ordre.
func (db *DB) NoteBackupAttempt(ctx context.Context, subject string, at time.Time, errKey, runID string) error {
	result, err := db.sql.ExecContext(ctx, `
		UPDATE backup_plans SET last_attempt_at = ?, last_error = ?, last_run_id = ? WHERE subject = ?`,
		at.Unix(), errKey, nullableString(runID), subject)
	if err != nil {
		return fmt.Errorf("note backup attempt: %w", err)
	}
	return notFoundIfNoRow(result, backup.ErrNotFound)
}

// SnapshotDatabase écrit dans un fichier neuf une copie cohérente de la base
// pendant qu'elle sert : VACUUM INTO lit d'un seul tenant, la copie est une
// base entière, sans fichier -wal à côté. Il refuse un fichier qui existe.
func (db *DB) SnapshotDatabase(ctx context.Context, path string) error {
	if _, err := db.sql.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		return fmt.Errorf("snapshot database: %w", err)
	}
	return nil
}
