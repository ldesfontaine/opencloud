package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
)

var _ audit.Store = (*DB)(nil)

// InsertAuditEvent ajoute une ligne au journal ; rien ne la modifie ensuite.
// Elle réserve dans la même transaction une livraison par canal qui reçoit
// le journal : une coupure ne laisse jamais un événement sans ses envois.
func (db *DB) InsertAuditEvent(ctx context.Context, event audit.Event) (int64, error) {
	details, err := encodeAuditDetails(event.Details)
	if err != nil {
		return 0, err
	}
	var id int64
	err = db.inTx(ctx, "insert audit event", func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			INSERT INTO audit_events (at, actor_kind, actor_name, action, object_kind, object_id, object_name, details, apply_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			event.At.Unix(), string(event.Actor.Kind), event.Actor.Name, string(event.Action),
			string(event.ObjectKind), event.ObjectID, event.ObjectName, details, nullableString(event.ApplyID))
		if err != nil {
			return fmt.Errorf("insert audit event: %w", err)
		}
		if id, err = result.LastInsertId(); err != nil {
			return fmt.Errorf("read audit event id: %w", err)
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO audit_deliveries (audit_id, channel_id, status, attempts, reason, code, created_at, updated_at)
			SELECT ?, id, 'pending', 0, '', 0, ?, ?
			FROM alert_channels WHERE enabled = 1 AND receives_audit = 1`,
			id, event.At.Unix(), event.At.Unix())
		if err != nil {
			return fmt.Errorf("reserve audit deliveries: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

const auditEventColumns = `id, at, actor_kind, actor_name, action, object_kind, object_id, object_name, details, apply_id`

// ListAuditEvents rend une page du journal, du plus récent au plus ancien.
func (db *DB) ListAuditEvents(ctx context.Context, query audit.Query) ([]audit.Event, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT `+auditEventColumns+`
		FROM audit_events
		WHERE (? = 0 OR id < ?)
		  AND (? = '' OR object_kind = ?)
		  AND (? = '' OR object_id = ?)
		ORDER BY id DESC
		LIMIT ?`,
		query.Before, query.Before,
		string(query.ObjectKind), string(query.ObjectKind),
		query.ObjectID, query.ObjectID,
		query.Limit)
	if err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	return collect(rows, scanAuditEvent)
}

// PurgeAuditEvents retire les lignes plus anciennes que before : la seule
// suppression du journal.
func (db *DB) PurgeAuditEvents(ctx context.Context, before time.Time) (int64, error) {
	result, err := db.sql.ExecContext(ctx, `DELETE FROM audit_events WHERE at < ?`, before.Unix())
	if err != nil {
		return 0, fmt.Errorf("purge audit events: %w", err)
	}
	return result.RowsAffected()
}

func scanAuditEvent(row scanner) (audit.Event, error) {
	var event audit.Event
	var at int64
	var actorKind, action, objectKind, details string
	var applyID sql.NullString
	err := row.Scan(&event.ID, &at, &actorKind, &event.Actor.Name, &action, &objectKind,
		&event.ObjectID, &event.ObjectName, &details, &applyID)
	if err != nil {
		return audit.Event{}, fmt.Errorf("scan audit event: %w", err)
	}
	event.At = time.Unix(at, 0).UTC()
	event.Actor.Kind = audit.ActorKind(actorKind)
	event.Action = audit.Action(action)
	event.ObjectKind = audit.ObjectKind(objectKind)
	event.ApplyID = applyID.String
	if err := json.Unmarshal([]byte(details), &event.Details); err != nil {
		return audit.Event{}, fmt.Errorf("decode audit details: %w", err)
	}
	return event, nil
}

// encodeAuditDetails rend toujours un objet : « {} » sans détail.
func encodeAuditDetails(details audit.Details) (string, error) {
	if details == nil {
		details = audit.Details{}
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		return "", fmt.Errorf("encode audit details: %w", err)
	}
	return string(encoded), nil
}
