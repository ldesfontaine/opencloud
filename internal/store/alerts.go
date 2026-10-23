package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/audit"
)

// --- Alertes ---

const alertColumns = `a.id, a.kind, a.severity, a.status, a.object_kind, a.object_id, a.object_name,
	COALESCE(a.machine_id, ''), COALESCE(m.name, ''), COALESCE(a.service_id, ''), a.details, a.opened_at, a.updated_at, a.resolved_at, a.acknowledged_at, a.clear_since`

const alertFrom = ` FROM alerts a LEFT JOIN machines m ON m.id = a.machine_id`

// objectColumns place l'identifiant de l'objet dans sa clé étrangère ; un
// volume ne porte que sa machine.
func objectColumns(found alert.Alert) (containerID, heartbeatID, probeID any) {
	switch found.Object.Kind {
	case alert.ObjectContainer:
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
	containerID, heartbeatID, probeID := objectColumns(found)
	result, err := db.sql.ExecContext(ctx, `
		INSERT INTO alerts (kind, severity, status, object_kind, object_id, object_name,
			machine_id, service_id, container_id, heartbeat_id, probe_id, details, opened_at, updated_at, resolved_at, acknowledged_at, clear_since)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(found.Kind), string(found.Severity), string(found.Status),
		string(found.Object.Kind), found.Object.ID, found.Object.Name,
		nullableString(found.MachineID), nullableString(found.ServiceID), containerID, heartbeatID, probeID, string(details),
		found.OpenedAt.Unix(), found.UpdatedAt.Unix(), nullableTime(found.ResolvedAt), nullableTime(found.AcknowledgedAt), nullableTime(found.ClearSince))
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
	if !filter.ClearBefore.IsZero() {
		query += ` AND a.clear_since IS NOT NULL AND a.clear_since <= ?`
		args = append(args, filter.ClearBefore.Unix())
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
		(a.object_kind = 'container' AND a.container_id IS NULL) OR
		(a.object_kind = 'heartbeat' AND a.heartbeat_id IS NULL) OR
		(a.object_kind = 'probe' AND a.probe_id IS NULL))`)
}

func (db *DB) UpdateAlert(ctx context.Context, found alert.Alert) error {
	details, err := json.Marshal(found.Details)
	if err != nil {
		return fmt.Errorf("encode alert details: %w", err)
	}
	result, err := db.sql.ExecContext(ctx, `
		UPDATE alerts SET kind = ?, severity = ?, status = ?, object_name = ?, machine_id = ?, service_id = ?, details = ?, updated_at = ?, resolved_at = ?,
			acknowledged_at = ?, clear_since = ?
		WHERE id = ?`,
		string(found.Kind), string(found.Severity), string(found.Status), found.Object.Name, nullableString(found.MachineID), nullableString(found.ServiceID), string(details),
		found.UpdatedAt.Unix(), nullableTime(found.ResolvedAt), nullableTime(found.AcknowledgedAt), nullableTime(found.ClearSince), found.ID)
	if err != nil {
		return fmt.Errorf("update alert: %w", err)
	}
	return notFoundIfNoRow(result, alert.ErrNotFound)
}

// AcknowledgeAlerts n'écrit qu'une instruction, donc une transaction :
// toute la sélection passe, ou rien. Seules les ouvertes non acquittées
// changent ; le reste est laissé tel quel.
func (db *DB) AcknowledgeAlerts(ctx context.Context, ids []int64, at time.Time) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	// Seuls les « ? » s'assemblent ; les identifiants passent en paramètres,
	// et leur nombre est borné par alert.MaxAcknowledgedAtOnce.
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(ids)), ", ")
	args := make([]any, 0, len(ids)+1)
	args = append(args, at.Unix())
	for _, id := range ids {
		args = append(args, id)
	}
	query := `UPDATE alerts SET acknowledged_at = ? WHERE status = 'open' AND acknowledged_at IS NULL AND id IN (` + placeholders + `)` // #nosec G202 -- des « ? » répétés, les valeurs sont liées.
	result, err := db.sql.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("acknowledge alerts: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count acknowledged alerts: %w", err)
	}
	return int(changed), nil // #nosec G115 -- au plus alert.MaxAcknowledgedAtOnce lignes.
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
	return collect(rows, scanAlert)
}

func scanAlert(row scanner) (alert.Alert, error) {
	var found alert.Alert
	var details string
	var openedAt, updatedAt int64
	var resolvedAt, acknowledgedAt, clearSince sql.NullInt64
	err := row.Scan(&found.ID, &found.Kind, &found.Severity, &found.Status, &found.Object.Kind, &found.Object.ID, &found.Object.Name,
		&found.MachineID, &found.MachineName, &found.ServiceID, &details, &openedAt, &updatedAt, &resolvedAt, &acknowledgedAt, &clearSince)
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
	found.ClearSince = timeOf(clearSince)
	return found, nil
}

// --- Canaux ---

const channelColumns = `id, name, url, format, destination, secret, min_severity, notify_resolve, receives_audit, enabled, created_at`

// InsertChannel crée le canal en deux temps dans une transaction : le chiffré
// de l'URL et du secret est lié à l'id de la ligne, qui n'existe qu'après
// l'INSERT.
func (db *DB) InsertChannel(ctx context.Context, channel alert.Channel) (int64, error) {
	var id int64
	err := db.inTx(ctx, "channel insert", func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
		INSERT INTO alert_channels (name, url, format, destination, secret, min_severity, notify_resolve, receives_audit, enabled, created_at)
		VALUES (?, '', ?, ?, '', ?, ?, ?, ?, ?)`,
			channel.Name, string(channel.Format), channel.Destination, string(channel.MinSeverity),
			channel.NotifyResolve, channel.ReceivesAudit, channel.Enabled, channel.CreatedAt.Unix())
		if err != nil {
			return fmt.Errorf("insert channel: %w", err)
		}
		id, err = result.LastInsertId()
		if err != nil {
			return fmt.Errorf("read channel id: %w", err)
		}
		channel.ID = id
		return db.writeChannelSecrets(ctx, tx, channel)
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

// writeChannelSecrets chiffre l'URL et le secret du canal pour sa ligne.
func (db *DB) writeChannelSecrets(ctx context.Context, tx *sql.Tx, channel alert.Channel) error {
	sealedURL, err := db.sealSecret(channel.URL, channelScope("url", channel.ID))
	if err != nil {
		return fmt.Errorf("seal channel url: %w", err)
	}
	sealedSecret, err := db.sealSecret(channel.Secret, channelScope("secret", channel.ID))
	if err != nil {
		return fmt.Errorf("seal channel secret: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE alert_channels SET url = ?, secret = ? WHERE id = ?`,
		sealedURL, sealedSecret, channel.ID); err != nil {
		return fmt.Errorf("write channel secrets: %w", err)
	}
	return nil
}

func (db *DB) ListChannels(ctx context.Context) ([]alert.Channel, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+channelColumns+` FROM alert_channels ORDER BY name, id`)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	return collect(rows, db.scanChannel)
}

func (db *DB) GetChannel(ctx context.Context, id int64) (alert.Channel, error) {
	return db.scanChannel(db.sql.QueryRowContext(ctx, `SELECT `+channelColumns+` FROM alert_channels WHERE id = ?`, id))
}

func (db *DB) UpdateChannel(ctx context.Context, channel alert.Channel) error {
	sealedURL, err := db.sealSecret(channel.URL, channelScope("url", channel.ID))
	if err != nil {
		return fmt.Errorf("seal channel url: %w", err)
	}
	sealedSecret, err := db.sealSecret(channel.Secret, channelScope("secret", channel.ID))
	if err != nil {
		return fmt.Errorf("seal channel secret: %w", err)
	}
	result, err := db.sql.ExecContext(ctx, `
		UPDATE alert_channels SET name = ?, url = ?, format = ?, destination = ?, secret = ?, min_severity = ?, notify_resolve = ?, receives_audit = ?, enabled = ? WHERE id = ?`,
		channel.Name, sealedURL, string(channel.Format), channel.Destination, sealedSecret, string(channel.MinSeverity), channel.NotifyResolve, channel.ReceivesAudit, channel.Enabled, channel.ID)
	if err != nil {
		return fmt.Errorf("update channel: %w", err)
	}
	return notFoundIfNoRow(result, alert.ErrNotFound)
}

func (db *DB) DeleteChannel(ctx context.Context, id int64) error {
	result, err := db.sql.ExecContext(ctx, `DELETE FROM alert_channels WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete channel: %w", err)
	}
	return notFoundIfNoRow(result, alert.ErrNotFound)
}

func (db *DB) scanChannel(row scanner) (alert.Channel, error) {
	var channel alert.Channel
	var createdAt int64
	err := row.Scan(&channel.ID, &channel.Name, &channel.URL, &channel.Format, &channel.Destination, &channel.Secret, &channel.MinSeverity,
		&channel.NotifyResolve, &channel.ReceivesAudit, &channel.Enabled, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return alert.Channel{}, alert.ErrNotFound
	}
	if err != nil {
		return alert.Channel{}, fmt.Errorf("scan channel: %w", err)
	}
	channel.URL, err = db.openSecret(channel.URL, channelScope("url", channel.ID))
	if err != nil {
		return alert.Channel{}, err
	}
	channel.Secret, err = db.openSecret(channel.Secret, channelScope("secret", channel.ID))
	if err != nil {
		return alert.Channel{}, err
	}
	channel.HasSecret = channel.Secret != ""
	channel.CreatedAt = time.Unix(createdAt, 0)
	return channel, nil
}

// --- Fenêtres pour taire ---

// La cible d'une fenêtre est l'une de ses clés étrangères ; son nom se lit
// à la lecture, l'objet ne peut pas manquer (cascade). Un service déclaré
// se nomme par son titre quand il en a un.
const muteColumns = `u.id, COALESCE(u.alert_kind, ''),
	CASE
		WHEN u.machine_id IS NOT NULL THEN 'machine'
		WHEN u.service_id IS NOT NULL THEN 'service'
		WHEN u.container_id IS NOT NULL THEN 'container'
		WHEN u.probe_id IS NOT NULL THEN 'probe'
		WHEN u.heartbeat_id IS NOT NULL THEN 'heartbeat'
		ELSE ''
	END,
	COALESCE(u.machine_id, u.service_id, u.container_id, u.probe_id, u.heartbeat_id, ''),
	COALESCE(m.name, NULLIF(s.title, ''), s.name, c.name, p.name, h.name, ''),
	u.reason, u.starts_at, u.ends_at, u.created_by, u.created_at,
	u.announce, u.public_title`

const muteFrom = ` FROM mutes u
	LEFT JOIN machines m ON m.id = u.machine_id
	LEFT JOIN declared_services s ON s.id = u.service_id
	LEFT JOIN containers c ON c.id = u.container_id
	LEFT JOIN probes p ON p.id = u.probe_id
	LEFT JOIN heartbeats h ON h.id = u.heartbeat_id`

// muteTargetColumns place l'identifiant de la cible dans sa clé
// étrangère ; aucune pour une fenêtre qui tait tout.
func muteTargetColumns(target alert.Target) (machineID, serviceID, containerID, probeID, heartbeatID any) {
	switch target.Kind {
	case alert.TargetMachine:
		return target.ID, nil, nil, nil, nil
	case alert.TargetService:
		return nil, target.ID, nil, nil, nil
	case alert.TargetContainer:
		return nil, nil, target.ID, nil, nil
	case alert.TargetProbe:
		return nil, nil, nil, target.ID, nil
	case alert.TargetHeartbeat:
		return nil, nil, nil, nil, target.ID
	}
	return nil, nil, nil, nil, nil
}

func (db *DB) InsertMute(ctx context.Context, mute alert.Mute) (int64, error) {
	machineID, serviceID, containerID, probeID, heartbeatID := muteTargetColumns(mute.Target)
	result, err := db.sql.ExecContext(ctx, `
		INSERT INTO mutes (alert_kind, machine_id, service_id, container_id, probe_id, heartbeat_id, reason, starts_at, ends_at, created_by, created_at, announce, public_title)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		nullableString(string(mute.Kind)), machineID, serviceID, containerID, probeID, heartbeatID,
		mute.Reason, mute.StartsAt.Unix(), mute.EndsAt.Unix(), actorText(mute.CreatedBy), mute.CreatedAt.Unix(),
		mute.Announce, mute.PublicTitle)
	if err != nil {
		return 0, fmt.Errorf("insert mute: %w", err)
	}
	return result.LastInsertId()
}

// MuteTargetName rend le nom de la cible, alert.ErrNotFound si elle n'existe pas.
func (db *DB) MuteTargetName(ctx context.Context, target alert.Target) (string, error) {
	var query string
	switch target.Kind {
	case alert.TargetMachine:
		query = `SELECT name FROM machines WHERE id = ?`
	case alert.TargetService:
		query = `SELECT COALESCE(NULLIF(title, ''), name) FROM declared_services WHERE id = ?`
	case alert.TargetContainer:
		query = `SELECT name FROM containers WHERE id = ?`
	case alert.TargetProbe:
		query = `SELECT name FROM probes WHERE id = ?`
	case alert.TargetHeartbeat:
		query = `SELECT name FROM heartbeats WHERE id = ?`
	default:
		return "", alert.ErrNotFound
	}
	var name string
	err := db.sql.QueryRowContext(ctx, query, target.ID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", alert.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read mute target: %w", err)
	}
	return name, nil
}

// ListMutes rend les fenêtres, celles qui finissent le plus tard d'abord.
func (db *DB) ListMutes(ctx context.Context) ([]alert.Mute, error) {
	return db.queryMutes(ctx, `SELECT `+muteColumns+muteFrom+` ORDER BY u.ends_at DESC, u.id DESC`)
}

func (db *DB) ListActiveMutes(ctx context.Context, at time.Time) ([]alert.Mute, error) {
	return db.queryMutes(ctx, `SELECT `+muteColumns+muteFrom+` WHERE u.starts_at <= ? AND u.ends_at > ? ORDER BY u.id`, at.Unix(), at.Unix())
}

func (db *DB) queryMutes(ctx context.Context, query string, args ...any) ([]alert.Mute, error) {
	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list mutes: %w", err)
	}
	return collect(rows, scanMute)
}

func scanMute(row scanner) (alert.Mute, error) {
	var mute alert.Mute
	var createdBy string
	var startsAt, endsAt, createdAt int64
	if err := row.Scan(&mute.ID, &mute.Kind, &mute.Target.Kind, &mute.Target.ID, &mute.Target.Name,
		&mute.Reason, &startsAt, &endsAt, &createdBy, &createdAt, &mute.Announce, &mute.PublicTitle); err != nil {
		return alert.Mute{}, fmt.Errorf("scan mute: %w", err)
	}
	mute.StartsAt = time.Unix(startsAt, 0)
	mute.EndsAt = time.Unix(endsAt, 0)
	mute.CreatedBy = actorOf(createdBy)
	mute.CreatedAt = time.Unix(createdAt, 0)
	return mute, nil
}

// actorText écrit l'acteur en une colonne, « genre:nom » ; actorOf le relit.
// Vide : aucun acteur connu.
func actorText(actor audit.Actor) string {
	if actor.Name == "" {
		return string(actor.Kind)
	}
	return string(actor.Kind) + ":" + actor.Name
}

func actorOf(text string) audit.Actor {
	kind, name, _ := strings.Cut(text, ":")
	return audit.Actor{Kind: audit.ActorKind(kind), Name: name}
}

// PurgeMutes efface les fenêtres finies avant l'instant donné.
func (db *DB) PurgeMutes(ctx context.Context, endedBefore time.Time) error {
	if _, err := db.sql.ExecContext(ctx, `DELETE FROM mutes WHERE ends_at < ?`, endedBefore.Unix()); err != nil {
		return fmt.Errorf("purge mutes: %w", err)
	}
	return nil
}

func (db *DB) DeleteMute(ctx context.Context, id int64) error {
	result, err := db.sql.ExecContext(ctx, `DELETE FROM mutes WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete mute: %w", err)
	}
	return notFoundIfNoRow(result, alert.ErrNotFound)
}

// --- Livraisons ---

const deliveryColumns = `id, alert_id, channel_id, event, status, attempts, reason, code, created_at, updated_at, next_attempt_at`

func (db *DB) InsertDelivery(ctx context.Context, delivery alert.Delivery) (int64, error) {
	result, err := db.sql.ExecContext(ctx, `
		INSERT INTO alert_deliveries (alert_id, channel_id, event, status, attempts, reason, code, created_at, updated_at, next_attempt_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		delivery.AlertID, delivery.ChannelID, string(delivery.Event), string(delivery.Status), delivery.Attempts,
		string(delivery.Reason), delivery.Code, delivery.CreatedAt.Unix(), delivery.UpdatedAt.Unix(), nullableTime(delivery.NextAttemptAt))
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
		UPDATE alert_deliveries SET status = ?, attempts = ?, reason = ?, code = ?, updated_at = ?, next_attempt_at = ? WHERE id = ?`,
		string(delivery.Status), delivery.Attempts, string(delivery.Reason), delivery.Code, delivery.UpdatedAt.Unix(),
		nullableTime(delivery.NextAttemptAt), delivery.ID)
	if err != nil {
		return fmt.Errorf("update delivery: %w", err)
	}
	return notFoundIfNoRow(result, alert.ErrNotFound)
}

func (db *DB) ListDeliveries(ctx context.Context, alertID int64) ([]alert.Delivery, error) {
	return db.queryDeliveries(ctx, `SELECT `+deliveryColumns+` FROM alert_deliveries WHERE alert_id = ? ORDER BY id`, alertID)
}

func (db *DB) ListPendingDeliveries(ctx context.Context) ([]alert.Delivery, error) {
	return db.queryDeliveries(ctx, `SELECT `+deliveryColumns+` FROM alert_deliveries WHERE status = 'pending' ORDER BY id`)
}

func (db *DB) ListDueDeliveries(ctx context.Context, at time.Time) ([]alert.Delivery, error) {
	return db.queryDeliveries(ctx, `SELECT `+deliveryColumns+` FROM alert_deliveries
		WHERE status = 'failed' AND next_attempt_at IS NOT NULL AND next_attempt_at <= ? ORDER BY id`, at.Unix())
}

func (db *DB) queryDeliveries(ctx context.Context, query string, args ...any) ([]alert.Delivery, error) {
	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list deliveries: %w", err)
	}
	return collect(rows, scanDelivery)
}

func scanDelivery(row scanner) (alert.Delivery, error) {
	var delivery alert.Delivery
	var createdAt, updatedAt int64
	var nextAttemptAt sql.NullInt64
	if err := row.Scan(&delivery.ID, &delivery.AlertID, &delivery.ChannelID, &delivery.Event, &delivery.Status, &delivery.Attempts,
		&delivery.Reason, &delivery.Code, &createdAt, &updatedAt, &nextAttemptAt); err != nil {
		return alert.Delivery{}, fmt.Errorf("scan delivery: %w", err)
	}
	delivery.CreatedAt = time.Unix(createdAt, 0)
	delivery.UpdatedAt = time.Unix(updatedAt, 0)
	delivery.NextAttemptAt = timeOf(nextAttemptAt)
	return delivery, nil
}
