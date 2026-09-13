package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
)

const machineColumns = `id, name, kind, public_key, hostname, address, os, arch, agent_version,
	enrolled_at, last_seen_at, created_at`

// SaveLocalMachine crée la machine openCloud ou rafraîchit ce qu'on sait
// d'elle ; sa date de création ne bouge pas.
func (db *DB) SaveLocalMachine(ctx context.Context, m machine.Machine) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO machines (`+machineColumns+`)
		VALUES (?, ?, ?, NULL, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			hostname = excluded.hostname, address = excluded.address, os = excluded.os,
			arch = excluded.arch, agent_version = excluded.agent_version,
			last_seen_at = excluded.last_seen_at`,
		m.ID, m.Name, string(m.Kind), m.Hostname, m.Address, m.OS, m.Arch, m.AgentVersion,
		m.EnrolledAt.Unix(), nullableTime(m.LastSeenAt), m.CreatedAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("save local machine: %w", err)
	}
	return nil
}

// ListMachines rend la machine openCloud d'abord, puis les autres par nom.
func (db *DB) ListMachines(ctx context.Context) ([]machine.Machine, error) {
	rows, err := db.sql.QueryContext(ctx,
		`SELECT `+machineColumns+` FROM machines ORDER BY kind = 'local' DESC, name`)
	if err != nil {
		return nil, fmt.Errorf("list machines: %w", err)
	}
	defer rows.Close()
	var machines []machine.Machine
	for rows.Next() {
		m, err := scanMachine(rows)
		if err != nil {
			return nil, err
		}
		machines = append(machines, m)
	}
	return machines, rows.Err()
}

func (db *DB) GetMachine(ctx context.Context, id string) (machine.Machine, error) {
	row := db.sql.QueryRowContext(ctx, `SELECT `+machineColumns+` FROM machines WHERE id = ?`, id)
	m, err := scanMachine(row)
	if errors.Is(err, sql.ErrNoRows) {
		return machine.Machine{}, machine.ErrNotFound
	}
	return m, err
}

func (db *DB) DeleteMachine(ctx context.Context, id string) error {
	result, err := db.sql.ExecContext(ctx, `DELETE FROM machines WHERE id = ? AND kind != 'local'`, id)
	if err != nil {
		return fmt.Errorf("delete machine: %w", err)
	}
	return notFoundIfNoRow(result)
}

func (db *DB) TouchMachine(ctx context.Context, id string, at time.Time) error {
	result, err := db.sql.ExecContext(ctx, `UPDATE machines SET last_seen_at = ? WHERE id = ?`, at.Unix(), id)
	if err != nil {
		return fmt.Errorf("touch machine: %w", err)
	}
	return notFoundIfNoRow(result)
}

// RecordConnection note l'adresse et la version vues à l'ouverture du flux :
// l'enrôlement les fige, la connexion les tient à jour.
func (db *DB) RecordConnection(ctx context.Context, id, address, agentVersion string, at time.Time) error {
	result, err := db.sql.ExecContext(ctx,
		`UPDATE machines SET address = ?, agent_version = ?, last_seen_at = ? WHERE id = ?`,
		address, agentVersion, at.Unix(), id)
	if err != nil {
		return fmt.Errorf("record connection: %w", err)
	}
	return notFoundIfNoRow(result)
}

// Enroll consomme le jeton et écrit la machine dans une seule transaction :
// deux agents avec le même jeton ne passent jamais tous les deux. Le jeton
// n'est consommé que par l'UPDATE conditionnel ; l'échec n'est classé
// qu'après, pour le message.
func (db *DB) Enroll(ctx context.Context, tokenHash string, candidate machine.Machine, now time.Time) (machine.Machine, error) {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return machine.Machine{}, fmt.Errorf("begin enroll: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		UPDATE enrollment_tokens SET consumed_at = ?, consumed_by = ?
		WHERE token_hash = ? AND consumed_at IS NULL AND expires_at > ?`,
		now.Unix(), candidate.ID, tokenHash, now.Unix())
	if err != nil {
		return machine.Machine{}, fmt.Errorf("consume token: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return machine.Machine{}, fmt.Errorf("consume token rows: %w", err)
	}
	if affected != 1 {
		return machine.Machine{}, classifyTokenFailure(ctx, tx, tokenHash, now)
	}

	var name string
	var existingID sql.NullString
	if err := tx.QueryRowContext(ctx,
		`SELECT name, machine_id FROM enrollment_tokens WHERE token_hash = ?`, tokenHash,
	).Scan(&name, &existingID); err != nil {
		return machine.Machine{}, fmt.Errorf("read token: %w", err)
	}
	candidate.Name = name
	if existingID.Valid {
		candidate.ID = existingID.String
		err = reenroll(ctx, tx, candidate)
	} else {
		err = insertMachine(ctx, tx, candidate)
	}
	if err != nil {
		return machine.Machine{}, err
	}
	if err := tx.Commit(); err != nil {
		return machine.Machine{}, fmt.Errorf("commit enroll: %w", err)
	}
	return db.GetMachine(ctx, candidate.ID)
}

func insertMachine(ctx context.Context, tx *sql.Tx, m machine.Machine) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO machines (`+machineColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.Name, string(m.Kind), m.PublicKey, m.Hostname, m.Address, m.OS, m.Arch, m.AgentVersion,
		m.EnrolledAt.Unix(), nullableTime(m.LastSeenAt), m.CreatedAt.Unix())
	if isUniqueViolation(err) {
		return machine.ErrNameTaken
	}
	if err != nil {
		return fmt.Errorf("insert machine: %w", err)
	}
	return nil
}

// Le ré-enrôlement remplace la clé et ce que l'agent dit de lui ; l'id, le
// nom et la date de création restent.
func reenroll(ctx context.Context, tx *sql.Tx, m machine.Machine) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE machines SET public_key = ?, hostname = ?, address = ?, os = ?, arch = ?,
			agent_version = ?, enrolled_at = ?, last_seen_at = NULL
		WHERE id = ? AND kind = 'remote'`,
		m.PublicKey, m.Hostname, m.Address, m.OS, m.Arch, m.AgentVersion, m.EnrolledAt.Unix(), m.ID)
	if err != nil {
		return fmt.Errorf("reenroll machine: %w", err)
	}
	return notFoundIfNoRow(result)
}

func classifyTokenFailure(ctx context.Context, tx *sql.Tx, tokenHash string, now time.Time) error {
	var consumedAt sql.NullInt64
	var expiresAt int64
	err := tx.QueryRowContext(ctx,
		`SELECT consumed_at, expires_at FROM enrollment_tokens WHERE token_hash = ?`, tokenHash,
	).Scan(&consumedAt, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return machine.ErrTokenNotFound
	}
	if err != nil {
		return fmt.Errorf("check token: %w", err)
	}
	if consumedAt.Valid {
		return machine.ErrTokenConsumed
	}
	if expiresAt <= now.Unix() {
		return machine.ErrTokenExpired
	}
	return machine.ErrTokenNotFound
}

func scanMachine(row scanner) (machine.Machine, error) {
	var m machine.Machine
	var kind string
	var publicKey []byte
	var enrolledAt, createdAt int64
	var lastSeenAt sql.NullInt64
	err := row.Scan(&m.ID, &m.Name, &kind, &publicKey, &m.Hostname, &m.Address, &m.OS, &m.Arch,
		&m.AgentVersion, &enrolledAt, &lastSeenAt, &createdAt)
	if err != nil {
		return machine.Machine{}, err
	}
	m.Kind = machine.Kind(kind)
	m.PublicKey = publicKey
	m.EnrolledAt = time.Unix(enrolledAt, 0)
	m.CreatedAt = time.Unix(createdAt, 0)
	if lastSeenAt.Valid {
		m.LastSeenAt = time.Unix(lastSeenAt.Int64, 0)
	}
	return m, nil
}
