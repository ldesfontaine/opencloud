package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
)

// --- Alertes ---

const alertColumns = `a.id, a.kind, a.severity, a.status, a.silenced, a.object_kind, a.object_id, a.object_name,
	COALESCE(a.machine_id, ''), COALESCE(m.name, ''), a.details, a.opened_at, a.updated_at, a.resolved_at, a.acknowledged_at`

const alertFrom = ` FROM alerts a LEFT JOIN machines m ON m.id = a.machine_id`

// objectColumns place l'identifiant de l'objet dans sa clé étrangère ; un
// volume ne porte que sa machine.
func objectColumns(found alert.Alert) (serviceID, heartbeatID, probeID any) {
	switch found.Object.Kind {
	case alert.ObjectService:
		return found.Object.ID, nil, nil
	case alert.ObjectHeartbeat:
		return nil, found.Object.ID, nil
	case alert.ObjectProbe:
		return nil, nil, found.Object.ID
	}
	return nil, nil, nil
}

func (db *DB) InsertAlert(ctx context.Context, found alert.Alert) (int64, error) {
	details, err := json.Marshal(found.Details)
	if err != nil {
		return 0, fmt.Errorf("encode alert details: %w", err)
	}
	serviceID, heartbeatID, probeID := objectColumns(found)
	result, err := db.sql.ExecContext(ctx, `
		INSERT INTO alerts (kind, severity, status, silenced, object_kind, object_id, object_name,
			machine_id, service_id, heartbeat_id, probe_id, details, opened_at, updated_at, resolved_at, acknowledged_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(found.Kind), string(found.Severity), string(found.Status), found.Silenced,
		string(found.Object.Kind), found.Object.ID, found.Object.Name,
		nullableString(found.MachineID), serviceID, heartbeatID, probeID, string(details),
		found.OpenedAt.Unix(), found.UpdatedAt.Unix(), nullableTime(found.ResolvedAt), nullableTime(found.AcknowledgedAt))
	if err != nil {
		return 0, fmt.Errorf("insert alert: %w", err)
	}
	return result.LastInsertId()
}

func (db *DB) GetAlert(ctx context.Context, id int64) (alert.Alert, error) {
	return scanAlert(db.sql.QueryRowContext(ctx, `SELECT `+alertColumns+alertFrom+` WHERE a.id = ?`, id))
}

func (db *DB) GetOpenAlert(ctx context.Context, kind alert.Kind, object alert.ObjectKind, objectID string) (alert.Alert, error) {
	return scanAlert(db.sql.QueryRowContext(ctx,
		`SELECT `+alertColumns+alertFrom+` WHERE a.status = 'open' AND a.kind = ? AND a.object_kind = ? AND a.object_id = ?`,
		string(kind), string(object), objectID))
}

// ListAlerts rend ce que le filtre retient : les ouvertes des plus graves
// aux plus récentes, les résolues des plus récentes aux plus anciennes.
func (db *DB) ListAlerts(ctx context.Context, filter alert.Filter) ([]alert.Alert, error) {
	query := `SELECT ` + alertColumns + alertFrom + ` WHERE 1 = 1`
	var args []any
	if filter.Status != "" {
		query += ` AND a.status = ?`
		args = append(args, string(filter.Status))
	}
	if filter.Kind != "" {
		query += ` AND a.kind = ?`
		args = append(args, string(filter.Kind))
	}
	if filter.ObjectKind != "" {
		query += ` AND a.object_kind = ?`
		args = append(args, string(filter.ObjectKind))
	}
	if filter.ObjectID != "" {
		query += ` AND a.object_id = ?`
		args = append(args, filter.ObjectID)
	}
	if filter.MachineID != "" {
		query += ` AND a.machine_id = ?`
		args = append(args, filter.MachineID)
	}
	if !filter.UpdatedBefore.IsZero() {
		query += ` AND a.updated_at < ?`
		args = append(args, filter.UpdatedBefore.Unix())
	}
	query += ` ORDER BY (a.status = 'open') DESC, (a.severity = 'danger') DESC, COALESCE(a.resolved_at, a.opened_at) DESC, a.id DESC`
	if filter.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, filter.Limit)
	}
	return db.queryAlerts(ctx, query, args...)
}

// ListOrphanAlerts rend les alertes ouvertes dont l'objet a disparu : sa
// clé étrangère est passée à NULL.
func (db *DB) ListOrphanAlerts(ctx context.Context) ([]alert.Alert, error) {
	return db.queryAlerts(ctx, `SELECT `+alertColumns+alertFrom+` WHERE a.status = 'open' AND (
		(a.object_kind IN ('machine', 'volume') AND a.machine_id IS NULL) OR
		(a.object_kind = 'service' AND a.service_id IS NULL) OR
		(a.object_kind = 'heartbeat' AND a.heartbeat_id IS NULL) OR
		(a.object_kind = 'probe' AND a.probe_id IS NULL))`)
}

func (db *DB) UpdateAlert(ctx context.Context, found alert.Alert) error {
	details, err := json.Marshal(found.Details)
	if err != nil {
		return fmt.Errorf("encode alert details: %w", err)
	}
	result, err := db.sql.ExecContext(ctx, `
		UPDATE alerts SET severity = ?, status = ?, object_name = ?, details = ?, updated_at = ?, resolved_at = ?, acknowledged_at = ?
		WHERE id = ?`,
		string(found.Severity), string(found.Status), found.Object.Name, string(details),
		found.UpdatedAt.Unix(), nullableTime(found.ResolvedAt), nullableTime(found.AcknowledgedAt), found.ID)
	if err != nil {
		return fmt.Errorf("update alert: %w", err)
	}
	return alertNotFoundIfNoRow(result)
}

func (db *DB) CountAlerts(ctx context.Context) (alert.Counts, error) {
	var counts alert.Counts
	err := db.sql.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(acknowledged_at IS NULL), 0) FROM alerts WHERE status = 'open'`).Scan(&counts.Open, &counts.Unacknowledged)
	if err != nil {
		return alert.Counts{}, fmt.Errorf("count alerts: %w", err)
	}
	return counts, nil
}

func (db *DB) PurgeAlerts(ctx context.Context, resolvedBefore time.Time) error {
	if _, err := db.sql.ExecContext(ctx, `DELETE FROM alerts WHERE resolved_at IS NOT NULL AND resolved_at < ?`, resolvedBefore.Unix()); err != nil {
		return fmt.Errorf("purge alerts: %w", err)
	}
	return nil
}

func (db *DB) queryAlerts(ctx context.Context, query string, args ...any) ([]alert.Alert, error) {
	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list alerts: %w", err)
	}
	defer rows.Close()
	alerts := []alert.Alert{}
	for rows.Next() {
		found, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		alerts = append(alerts, found)
	}
	return alerts, rows.Err()
}

func scanAlert(row scanner) (alert.Alert, error) {
	var found alert.Alert
	var details string
	var openedAt, updatedAt int64
	var resolvedAt, acknowledgedAt sql.NullInt64
	err := row.Scan(&found.ID, &found.Kind, &found.Severity, &found.Status, &found.Silenced, &found.Object.Kind, &found.Object.ID, &found.Object.Name,
		&found.MachineID, &found.MachineName, &details, &openedAt, &updatedAt, &resolvedAt, &acknowledgedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return alert.Alert{}, alert.ErrNotFound
	}
	if err != nil {
		return alert.Alert{}, fmt.Errorf("scan alert: %w", err)
	}
	if err := json.Unmarshal([]byte(details), &found.Details); err != nil {
		return alert.Alert{}, fmt.Errorf("decode alert details: %w", err)
	}
	found.OpenedAt = time.Unix(openedAt, 0)
	found.UpdatedAt = time.Unix(updatedAt, 0)
	found.ResolvedAt = timeOf(resolvedAt)
	found.AcknowledgedAt = timeOf(acknowledgedAt)
	return found, nil
}

func alertNotFoundIfNoRow(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return alert.ErrNotFound
	}
	return nil
}

// --- Canaux ---

const channelColumns = `id, name, url, format, secret, min_severity, notify_resolve, enabled, created_at`

func (db *DB) InsertChannel(ctx context.Context, channel alert.Channel) (int64, error) {
	result, err := db.sql.ExecContext(ctx, `
		INSERT INTO alert_channels (name, url, format, secret, min_severity, notify_resolve, enabled, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		channel.Name, channel.URL, string(channel.Format), channel.Secret, string(channel.MinSeverity),
		channel.NotifyResolve, channel.Enabled, channel.CreatedAt.Unix())
	if err != nil {
		return 0, fmt.Errorf("insert channel: %w", err)
	}
	return result.LastInsertId()
}

func (db *DB) ListChannels(ctx context.Context) ([]alert.Channel, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+channelColumns+` FROM alert_channels ORDER BY name, id`)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	defer rows.Close()
	channels := []alert.Channel{}
	for rows.Next() {
		channel, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		channels = append(channels, channel)
	}
	return channels, rows.Err()
}

func (db *DB) GetChannel(ctx context.Context, id int64) (alert.Channel, error) {
	return scanChannel(db.sql.QueryRowContext(ctx, `SELECT `+channelColumns+` FROM alert_channels WHERE id = ?`, id))
}

func (db *DB) UpdateChannel(ctx context.Context, channel alert.Channel) error {
	result, err := db.sql.ExecContext(ctx, `
		UPDATE alert_channels SET name = ?, url = ?, format = ?, secret = ?, min_severity = ?, notify_resolve = ?, enabled = ? WHERE id = ?`,
		channel.Name, channel.URL, string(channel.Format), channel.Secret, string(channel.MinSeverity), channel.NotifyResolve, channel.Enabled, channel.ID)
	if err != nil {
		return fmt.Errorf("update channel: %w", err)
	}
	return alertNotFoundIfNoRow(result)
}

func (db *DB) DeleteChannel(ctx context.Context, id int64) error {
	result, err := db.sql.ExecContext(ctx, `DELETE FROM alert_channels WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete channel: %w", err)
	}
	return alertNotFoundIfNoRow(result)
}

func scanChannel(row scanner) (alert.Channel, error) {
	var channel alert.Channel
	var createdAt int64
	err := row.Scan(&channel.ID, &channel.Name, &channel.URL, &channel.Format, &channel.Secret, &channel.MinSeverity,
		&channel.NotifyResolve, &channel.Enabled, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return alert.Channel{}, alert.ErrNotFound
	}
	if err != nil {
		return alert.Channel{}, fmt.Errorf("scan channel: %w", err)
	}
	channel.HasSecret = channel.Secret != ""
	channel.CreatedAt = time.Unix(createdAt, 0)
	return channel, nil
}

// --- Silences ---

func (db *DB) InsertSilence(ctx context.Context, silence alert.Silence) (int64, error) {
	result, err := db.sql.ExecContext(ctx, `
		INSERT INTO alert_silences (kind, object_kind, object_id, object_name, reason, starts_at, ends_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		string(silence.Kind), string(silence.Object.Kind), silence.Object.ID, silence.Object.Name, silence.Reason,
		silence.StartsAt.Unix(), silence.EndsAt.Unix(), silence.CreatedAt.Unix())
	if err != nil {
		return 0, fmt.Errorf("insert silence: %w", err)
	}
	return result.LastInsertId()
}

// ListSilences rend les silences, ceux qui finissent le plus tard d'abord.
func (db *DB) ListSilences(ctx context.Context) ([]alert.Silence, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT id, kind, object_kind, object_id, object_name, reason, starts_at, ends_at, created_at
		FROM alert_silences ORDER BY ends_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list silences: %w", err)
	}
	defer rows.Close()
	silences := []alert.Silence{}
	for rows.Next() {
		var silence alert.Silence
		var startsAt, endsAt, createdAt int64
		if err := rows.Scan(&silence.ID, &silence.Kind, &silence.Object.Kind, &silence.Object.ID, &silence.Object.Name, &silence.Reason, &startsAt, &endsAt, &createdAt); err != nil {
			return nil, fmt.Errorf("scan silence: %w", err)
		}
		silence.StartsAt = time.Unix(startsAt, 0)
		silence.EndsAt = time.Unix(endsAt, 0)
		silence.CreatedAt = time.Unix(createdAt, 0)
		silences = append(silences, silence)
	}
	return silences, rows.Err()
}

func (db *DB) DeleteSilence(ctx context.Context, id int64) error {
	result, err := db.sql.ExecContext(ctx, `DELETE FROM alert_silences WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete silence: %w", err)
	}
	return alertNotFoundIfNoRow(result)
}

// --- Livraisons ---

const deliveryColumns = `id, alert_id, channel_id, event, status, attempts, reason, code, created_at, updated_at`

func (db *DB) InsertDelivery(ctx context.Context, delivery alert.Delivery) (int64, error) {
	result, err := db.sql.ExecContext(ctx, `
		INSERT INTO alert_deliveries (alert_id, channel_id, event, status, attempts, reason, code, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		delivery.AlertID, delivery.ChannelID, string(delivery.Event), string(delivery.Status), delivery.Attempts,
		string(delivery.Reason), delivery.Code, delivery.CreatedAt.Unix(), delivery.UpdatedAt.Unix())
	if isUniqueViolation(err) {
		return 0, alert.ErrDeliveryExists
	}
	if err != nil {
		return 0, fmt.Errorf("insert delivery: %w", err)
	}
	return result.LastInsertId()
}

func (db *DB) UpdateDelivery(ctx context.Context, delivery alert.Delivery) error {
	result, err := db.sql.ExecContext(ctx, `
		UPDATE alert_deliveries SET status = ?, attempts = ?, reason = ?, code = ?, updated_at = ? WHERE id = ?`,
		string(delivery.Status), delivery.Attempts, string(delivery.Reason), delivery.Code, delivery.UpdatedAt.Unix(), delivery.ID)
	if err != nil {
		return fmt.Errorf("update delivery: %w", err)
	}
	return alertNotFoundIfNoRow(result)
}

func (db *DB) ListDeliveries(ctx context.Context, alertID int64) ([]alert.Delivery, error) {
	return db.queryDeliveries(ctx, `SELECT `+deliveryColumns+` FROM alert_deliveries WHERE alert_id = ? ORDER BY id`, alertID)
}

func (db *DB) ListPendingDeliveries(ctx context.Context) ([]alert.Delivery, error) {
	return db.queryDeliveries(ctx, `SELECT `+deliveryColumns+` FROM alert_deliveries WHERE status = 'pending' ORDER BY id`)
}

func (db *DB) queryDeliveries(ctx context.Context, query string, args ...any) ([]alert.Delivery, error) {
	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list deliveries: %w", err)
	}
	defer rows.Close()
	deliveries := []alert.Delivery{}
	for rows.Next() {
		var delivery alert.Delivery
		var createdAt, updatedAt int64
		if err := rows.Scan(&delivery.ID, &delivery.AlertID, &delivery.ChannelID, &delivery.Event, &delivery.Status, &delivery.Attempts,
			&delivery.Reason, &delivery.Code, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan delivery: %w", err)
		}
		delivery.CreatedAt = time.Unix(createdAt, 0)
		delivery.UpdatedAt = time.Unix(updatedAt, 0)
		deliveries = append(deliveries, delivery)
	}
	return deliveries, rows.Err()
}
