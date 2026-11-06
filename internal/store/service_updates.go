package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/deploy"
)

// --- Montées de version ---

const serviceUpdateColumns = `run_id, COALESCE(service_id, ''), machine_id, unit_id, unit_name, from_version, to_version, state, reason, requested_at, settled_at`

func (db *DB) InsertServiceUpdate(ctx context.Context, update deploy.ServiceUpdate) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO service_updates (run_id, service_id, machine_id, unit_id, unit_name, from_version, to_version, state, reason, requested_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		update.RunID, nullableString(update.ServiceID), update.MachineID, update.UnitID, update.UnitName,
		update.FromVersion, update.ToVersion, string(update.State), update.Reason, update.RequestedAt.Unix())
	if err != nil {
		return fmt.Errorf("insert service update: %w", err)
	}
	return nil
}

// SettleServiceUpdate ferme la montée en attente de ce run. Une montée déjà
// fermée, ou un run sans montée (un redéploiement), rend faux : un rapport
// rejoué ne ferme rien deux fois.
func (db *DB) SettleServiceUpdate(ctx context.Context, runID string, state deploy.UpdateState, reason string, at time.Time) (deploy.ServiceUpdate, bool, error) {
	result, err := db.sql.ExecContext(ctx,
		`UPDATE service_updates SET state = ?, reason = ?, settled_at = ? WHERE run_id = ? AND state = 'pending'`,
		string(state), reason, at.Unix(), runID)
	if err != nil {
		return deploy.ServiceUpdate{}, false, fmt.Errorf("settle service update: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed == 0 {
		return deploy.ServiceUpdate{}, false, err
	}
	update, err := scanServiceUpdate(db.sql.QueryRowContext(ctx, `SELECT `+serviceUpdateColumns+` FROM service_updates WHERE run_id = ?`, runID))
	if err != nil {
		return deploy.ServiceUpdate{}, false, err
	}
	return update, true, nil
}

func (db *DB) FindPendingServiceUpdate(ctx context.Context, machineID string, since time.Time) (deploy.ServiceUpdate, bool, error) {
	update, err := scanServiceUpdate(db.sql.QueryRowContext(ctx,
		`SELECT `+serviceUpdateColumns+` FROM service_updates WHERE machine_id = ? AND state = 'pending' AND requested_at >= ? LIMIT 1`,
		machineID, since.Unix()))
	return foundOrNone(update, err)
}

func (db *DB) LatestServiceUpdate(ctx context.Context, serviceID string) (deploy.ServiceUpdate, bool, error) {
	update, err := scanServiceUpdate(db.sql.QueryRowContext(ctx,
		`SELECT `+serviceUpdateColumns+` FROM service_updates WHERE service_id = ? ORDER BY requested_at DESC, rowid DESC LIMIT 1`,
		serviceID))
	return foundOrNone(update, err)
}

func foundOrNone(update deploy.ServiceUpdate, err error) (deploy.ServiceUpdate, bool, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return deploy.ServiceUpdate{}, false, nil
	}
	if err != nil {
		return deploy.ServiceUpdate{}, false, err
	}
	return update, true, nil
}

func scanServiceUpdate(row scanner) (deploy.ServiceUpdate, error) {
	var update deploy.ServiceUpdate
	var state string
	var requestedAt int64
	var settledAt sql.NullInt64
	err := row.Scan(&update.RunID, &update.ServiceID, &update.MachineID, &update.UnitID, &update.UnitName,
		&update.FromVersion, &update.ToVersion, &state, &update.Reason, &requestedAt, &settledAt)
	if err != nil {
		return deploy.ServiceUpdate{}, err
	}
	update.State = deploy.UpdateState(state)
	update.RequestedAt = time.Unix(requestedAt, 0)
	update.SettledAt = timeOrZero(settledAt)
	return update, nil
}
