package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/audit"
)

// Les livraisons du journal : une ligne par (événement, canal), réservée avec
// l'événement (voir InsertAuditEvent) et jouée par le notifieur des alertes.
const auditDeliveryColumns = `id, audit_id, channel_id, status, attempts, reason, code, created_at, updated_at, next_attempt_at`

func (db *DB) ListAuditDeliveries(ctx context.Context, auditID int64) ([]alert.Delivery, error) {
	return db.queryAuditDeliveries(ctx, `SELECT `+auditDeliveryColumns+` FROM audit_deliveries WHERE audit_id = ? ORDER BY id`, auditID)
}

func (db *DB) ListPendingAuditDeliveries(ctx context.Context) ([]alert.Delivery, error) {
	return db.queryAuditDeliveries(ctx, `SELECT `+auditDeliveryColumns+` FROM audit_deliveries WHERE status = 'pending' ORDER BY id`)
}

func (db *DB) ListDueAuditDeliveries(ctx context.Context, at time.Time) ([]alert.Delivery, error) {
	return db.queryAuditDeliveries(ctx, `SELECT `+auditDeliveryColumns+` FROM audit_deliveries
		WHERE status = 'failed' AND next_attempt_at IS NOT NULL AND next_attempt_at <= ? ORDER BY id`, at.Unix())
}

func (db *DB) UpdateAuditDelivery(ctx context.Context, delivery alert.Delivery) error {
	result, err := db.sql.ExecContext(ctx, `
		UPDATE audit_deliveries SET status = ?, attempts = ?, reason = ?, code = ?, updated_at = ?, next_attempt_at = ? WHERE id = ?`,
		string(delivery.Status), delivery.Attempts, string(delivery.Reason), delivery.Code, delivery.UpdatedAt.Unix(),
		nullableTime(delivery.NextAttemptAt), delivery.ID)
	if err != nil {
		return fmt.Errorf("update audit delivery: %w", err)
	}
	return notFoundIfNoRow(result, alert.ErrNotFound)
}

// GetAuditEvent relit un événement pour le rejouer. Absent (purgé entre-temps),
// il rend sql.ErrNoRows : l'appelant passe à la livraison suivante.
func (db *DB) GetAuditEvent(ctx context.Context, id int64) (audit.Event, error) {
	event, err := scanAuditEvent(db.sql.QueryRowContext(ctx, `SELECT `+auditEventColumns+` FROM audit_events WHERE id = ?`, id))
	if err != nil {
		return audit.Event{}, fmt.Errorf("get audit event %d: %w", id, err)
	}
	return event, nil
}

// CountAuditForwarding dit combien de canaux reçoivent le journal et
// combien de livraisons sont en échec et encore rejouées : de quoi alerter
// l'opérateur. Une livraison abandonnée (24 h passées) n'y compte plus, sans
// quoi une panne d'une heure laisserait l'avertissement un an, jusqu'à la
// purge de son événement ; le journal du serveur dit l'abandon.
func (db *DB) CountAuditForwarding(ctx context.Context) (channels int, failing int, err error) {
	err = db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_channels WHERE enabled = 1 AND receives_audit = 1`).Scan(&channels)
	if err != nil {
		return 0, 0, fmt.Errorf("count audit channels: %w", err)
	}
	err = db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_deliveries WHERE status = 'failed' AND next_attempt_at IS NOT NULL`).Scan(&failing)
	if err != nil {
		return 0, 0, fmt.Errorf("count failing audit deliveries: %w", err)
	}
	return channels, failing, nil
}

func (db *DB) queryAuditDeliveries(ctx context.Context, query string, args ...any) ([]alert.Delivery, error) {
	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list audit deliveries: %w", err)
	}
	return collect(rows, scanAuditDelivery)
}

func scanAuditDelivery(row scanner) (alert.Delivery, error) {
	delivery := alert.Delivery{Event: alert.EventAudit}
	var createdAt, updatedAt int64
	var nextAttemptAt sql.NullInt64
	if err := row.Scan(&delivery.ID, &delivery.AuditID, &delivery.ChannelID, &delivery.Status, &delivery.Attempts,
		&delivery.Reason, &delivery.Code, &createdAt, &updatedAt, &nextAttemptAt); err != nil {
		return alert.Delivery{}, fmt.Errorf("scan audit delivery: %w", err)
	}
	delivery.CreatedAt = time.Unix(createdAt, 0)
	delivery.UpdatedAt = time.Unix(updatedAt, 0)
	delivery.NextAttemptAt = timeOf(nextAttemptAt)
	return delivery, nil
}
