package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/firewall"
)

var _ firewall.Store = (*DB)(nil)

const firewallColumns = `id, name, kind, api_url, api_key, api_secret, pin, version, verified_at, last_read_at, last_error, created_at`

// scanFirewall lit le pare-feu et son secret tel que la base le garde, scellé.
func scanFirewall(row scanner) (declared firewall.Firewall, sealedSecret string, err error) {
	var verifiedAt, createdAt int64
	var lastReadAt sql.NullInt64
	err = row.Scan(&declared.ID, &declared.Name, &declared.Kind, &declared.APIURL, &declared.APIKey, &sealedSecret,
		&declared.Pin, &declared.Version, &verifiedAt, &lastReadAt, &declared.LastError, &createdAt)
	declared.VerifiedAt = time.Unix(verifiedAt, 0)
	declared.LastReadAt = timeOf(lastReadAt)
	declared.CreatedAt = time.Unix(createdAt, 0)
	return declared, sealedSecret, err
}

// GetFirewallCredentials rend le pare-feu avec son secret ouvert : pour parler
// à l'API, jamais pour une réponse.
func (db *DB) GetFirewallCredentials(ctx context.Context, id string) (firewall.Credentials, error) {
	declared, sealed, err := scanFirewall(db.sql.QueryRowContext(ctx,
		`SELECT `+firewallColumns+` FROM firewalls WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return firewall.Credentials{}, firewall.ErrNotFound
	}
	if err != nil {
		return firewall.Credentials{}, fmt.Errorf("get firewall: %w", err)
	}
	secret, err := db.openSecret(sealed, firewallSecretScope(id))
	if err != nil {
		return firewall.Credentials{}, fmt.Errorf("open firewall secret: %w", err)
	}
	return firewall.Credentials{Firewall: declared, APISecret: secret}, nil
}

// CreateFirewall écrit un pare-feu neuf. Compter puis insérer tient dans la
// transaction : deux déclarations qui se croisent ne passent pas le plafond.
func (db *DB) CreateFirewall(ctx context.Context, credentials firewall.Credentials) error {
	sealed, err := db.sealSecret(credentials.APISecret, firewallSecretScope(credentials.ID))
	if err != nil {
		return fmt.Errorf("seal firewall secret: %w", err)
	}
	return db.inTx(ctx, "create firewall", func(tx *sql.Tx) error {
		var declared int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM firewalls`).Scan(&declared); err != nil {
			return fmt.Errorf("count firewalls: %w", err)
		}
		if declared >= firewall.MaxFirewalls {
			return firewall.ErrTooMany
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO firewalls (id, name, kind, api_url, api_key, api_secret, pin, version, verified_at, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			credentials.ID, credentials.Name, credentials.Kind, credentials.APIURL, credentials.APIKey, sealed,
			credentials.Pin, credentials.Version, credentials.VerifiedAt.Unix(), credentials.CreatedAt.Unix())
		if isUniqueViolation(err) {
			return firewall.ErrNameTaken
		}
		if err != nil {
			return fmt.Errorf("insert firewall: %w", err)
		}
		return nil
	})
}

// UpdateFirewall remplace la connexion. Une autre adresse d'API ou un autre
// genre efface le relevé : il décrivait un autre appareil.
func (db *DB) UpdateFirewall(ctx context.Context, credentials firewall.Credentials) error {
	sealed, err := db.sealSecret(credentials.APISecret, firewallSecretScope(credentials.ID))
	if err != nil {
		return fmt.Errorf("seal firewall secret: %w", err)
	}
	return db.inTx(ctx, "update firewall", func(tx *sql.Tx) error {
		var previousURL, previousKind string
		err := tx.QueryRowContext(ctx, `SELECT api_url, kind FROM firewalls WHERE id = ?`, credentials.ID).
			Scan(&previousURL, &previousKind)
		if errors.Is(err, sql.ErrNoRows) {
			return firewall.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get firewall connection: %w", err)
		}
		if previousURL != credentials.APIURL || previousKind != credentials.Kind {
			if err := clearFirewallSnapshot(ctx, tx, credentials.ID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE firewalls SET last_read_at = NULL WHERE id = ?`, credentials.ID); err != nil {
				return fmt.Errorf("reset firewall read date: %w", err)
			}
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE firewalls SET name = ?, kind = ?, api_url = ?, api_key = ?, api_secret = ?, pin = ?, version = ?,
				verified_at = ?, last_error = ''
			WHERE id = ?`,
			credentials.Name, credentials.Kind, credentials.APIURL, credentials.APIKey, sealed, credentials.Pin,
			credentials.Version, credentials.VerifiedAt.Unix(), credentials.ID)
		if isUniqueViolation(err) {
			return firewall.ErrNameTaken
		}
		if err != nil {
			return fmt.Errorf("update firewall: %w", err)
		}
		return nil
	})
}

// DeleteFirewall retire le pare-feu ; son relevé part avec lui.
func (db *DB) DeleteFirewall(ctx context.Context, id string) error {
	result, err := db.sql.ExecContext(ctx, `DELETE FROM firewalls WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete firewall: %w", err)
	}
	return notFoundIfNoRow(result, firewall.ErrNotFound)
}

func (db *DB) ListFirewallIDs(ctx context.Context) ([]string, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT id FROM firewalls ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list firewall ids: %w", err)
	}
	return collect(rows, func(row scanner) (string, error) {
		var id string
		err := row.Scan(&id)
		return id, err
	})
}

// NoteFirewallFailure ne touche qu'à la clé du refus : le dernier relevé
// réussi reste ce que la fiche montre.
func (db *DB) NoteFirewallFailure(ctx context.Context, id, errorKey string) error {
	result, err := db.sql.ExecContext(ctx, `UPDATE firewalls SET last_error = ? WHERE id = ?`, errorKey, id)
	if err != nil {
		return fmt.Errorf("note firewall failure: %w", err)
	}
	return notFoundIfNoRow(result, firewall.ErrNotFound)
}

// ReplaceFirewallSnapshot remplace en une transaction tout ce que le pare-feu
// avait relevé : ce qu'un relevé ne montre plus disparaît.
func (db *DB) ReplaceFirewallSnapshot(ctx context.Context, id string, snapshot firewall.Snapshot, at time.Time) error {
	return db.inTx(ctx, "replace firewall snapshot", func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE firewalls SET version = ?, last_read_at = ?, last_error = '' WHERE id = ?`,
			snapshot.Version, at.Unix(), id)
		if err != nil {
			return fmt.Errorf("update firewall: %w", err)
		}
		if err := notFoundIfNoRow(result, firewall.ErrNotFound); err != nil {
			return err
		}
		if err := clearFirewallSnapshot(ctx, tx, id); err != nil {
			return err
		}
		return insertFirewallSnapshot(ctx, tx, id, snapshot)
	})
}

func clearFirewallSnapshot(ctx context.Context, tx *sql.Tx, id string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM firewall_interfaces WHERE firewall_id = ?`, id); err != nil {
		return fmt.Errorf("delete firewall interfaces: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM firewall_rules WHERE firewall_id = ?`, id); err != nil {
		return fmt.Errorf("delete firewall rules: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM firewall_leases WHERE firewall_id = ?`, id); err != nil {
		return fmt.Errorf("delete firewall leases: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM firewall_gateways WHERE firewall_id = ?`, id); err != nil {
		return fmt.Errorf("delete firewall gateways: %w", err)
	}
	return nil
}

func insertFirewallSnapshot(ctx context.Context, tx *sql.Tx, id string, snapshot firewall.Snapshot) error {
	for _, item := range snapshot.Interfaces {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO firewall_interfaces (firewall_id, name, device, role, address, status) VALUES (?, ?, ?, ?, ?, ?)`,
			id, item.Name, item.Device, item.Role, item.Address, item.Status); err != nil {
			return fmt.Errorf("insert firewall interface %s: %w", item.Name, err)
		}
	}
	for position, rule := range snapshot.Rules {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO firewall_rules (firewall_id, position, uuid, interface, action, direction, protocol, source,
				source_port, destination, destination_port, description, enabled)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, position, rule.UUID, rule.Interface, rule.Action, rule.Direction, rule.Protocol, rule.Source,
			rule.SourcePort, rule.Destination, rule.DestinationPort, rule.Description, rule.Enabled); err != nil {
			return fmt.Errorf("insert firewall rule %d: %w", position, err)
		}
	}
	for position, lease := range snapshot.Leases {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO firewall_leases (firewall_id, position, address, mac, hostname, interface, server, is_static, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, position, lease.Address, lease.MAC, lease.Hostname, lease.Interface, lease.Server, lease.IsStatic,
			nullableTime(lease.ExpiresAt)); err != nil {
			return fmt.Errorf("insert firewall lease %d: %w", position, err)
		}
	}
	for _, gateway := range snapshot.Gateways {
		var delay, loss any
		if gateway.IsMonitored {
			delay, loss = gateway.DelayMillis, gateway.LossPercent
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO firewall_gateways (firewall_id, name, address, status, delay_ms, loss_percent) VALUES (?, ?, ?, ?, ?, ?)`,
			id, gateway.Name, gateway.Address, gateway.Status, delay, loss); err != nil {
			return fmt.Errorf("insert firewall gateway %s: %w", gateway.Name, err)
		}
	}
	return nil
}

// ListFirewalls rend chaque pare-feu avec le compte de ce qu'il a relevé,
// jamais son secret.
func (db *DB) ListFirewalls(ctx context.Context) ([]firewall.Summary, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT f.id, f.name, f.kind, f.api_url, f.api_key, f.pin, f.version, f.verified_at, f.last_read_at,
			f.last_error, f.created_at,
			(SELECT COUNT(*) FROM firewall_interfaces WHERE firewall_id = f.id),
			(SELECT COUNT(*) FROM firewall_rules WHERE firewall_id = f.id),
			(SELECT COUNT(*) FROM firewall_leases WHERE firewall_id = f.id),
			(SELECT COUNT(*) FROM firewall_gateways WHERE firewall_id = f.id)
		FROM firewalls f ORDER BY f.name`)
	if err != nil {
		return nil, fmt.Errorf("list firewalls: %w", err)
	}
	return collect(rows, func(row scanner) (firewall.Summary, error) {
		var summary firewall.Summary
		var verifiedAt, createdAt int64
		var lastReadAt sql.NullInt64
		err := row.Scan(&summary.ID, &summary.Name, &summary.Kind, &summary.APIURL, &summary.APIKey,
			&summary.Pin, &summary.Version, &verifiedAt, &lastReadAt, &summary.LastError, &createdAt,
			&summary.Interfaces, &summary.Rules, &summary.Leases, &summary.Gateways)
		summary.VerifiedAt = time.Unix(verifiedAt, 0)
		summary.LastReadAt = timeOf(lastReadAt)
		summary.CreatedAt = time.Unix(createdAt, 0)
		return summary, err
	})
}

// GetFirewallView rend un pare-feu avec son dernier relevé, jamais son secret.
func (db *DB) GetFirewallView(ctx context.Context, id string) (firewall.View, error) {
	declared, _, err := scanFirewall(db.sql.QueryRowContext(ctx,
		`SELECT `+firewallColumns+` FROM firewalls WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return firewall.View{}, firewall.ErrNotFound
	}
	if err != nil {
		return firewall.View{}, fmt.Errorf("get firewall: %w", err)
	}
	view := firewall.View{Firewall: declared}
	if view.Interfaces, err = db.listFirewallInterfaces(ctx, id); err != nil {
		return firewall.View{}, err
	}
	if view.Rules, err = db.listFirewallRules(ctx, id); err != nil {
		return firewall.View{}, err
	}
	if view.Leases, err = db.listFirewallLeases(ctx, id); err != nil {
		return firewall.View{}, err
	}
	if view.Gateways, err = db.listFirewallGateways(ctx, id); err != nil {
		return firewall.View{}, err
	}
	return view, nil
}

func (db *DB) listFirewallInterfaces(ctx context.Context, id string) ([]firewall.Interface, error) {
	rows, err := db.sql.QueryContext(ctx,
		`SELECT name, device, role, address, status FROM firewall_interfaces WHERE firewall_id = ? ORDER BY name`, id)
	if err != nil {
		return nil, fmt.Errorf("list firewall interfaces: %w", err)
	}
	return collect(rows, func(row scanner) (firewall.Interface, error) {
		var item firewall.Interface
		err := row.Scan(&item.Name, &item.Device, &item.Role, &item.Address, &item.Status)
		return item, err
	})
}

func (db *DB) listFirewallRules(ctx context.Context, id string) ([]firewall.Rule, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT uuid, interface, action, direction, protocol, source, source_port, destination, destination_port,
			description, enabled
		FROM firewall_rules WHERE firewall_id = ? ORDER BY position`, id)
	if err != nil {
		return nil, fmt.Errorf("list firewall rules: %w", err)
	}
	return collect(rows, func(row scanner) (firewall.Rule, error) {
		var rule firewall.Rule
		err := row.Scan(&rule.UUID, &rule.Interface, &rule.Action, &rule.Direction, &rule.Protocol, &rule.Source,
			&rule.SourcePort, &rule.Destination, &rule.DestinationPort, &rule.Description, &rule.Enabled)
		return rule, err
	})
}

func (db *DB) listFirewallLeases(ctx context.Context, id string) ([]firewall.Lease, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT address, mac, hostname, interface, server, is_static, expires_at
		FROM firewall_leases WHERE firewall_id = ? ORDER BY position`, id)
	if err != nil {
		return nil, fmt.Errorf("list firewall leases: %w", err)
	}
	return collect(rows, func(row scanner) (firewall.Lease, error) {
		var lease firewall.Lease
		var expiresAt sql.NullInt64
		err := row.Scan(&lease.Address, &lease.MAC, &lease.Hostname, &lease.Interface, &lease.Server, &lease.IsStatic, &expiresAt)
		lease.ExpiresAt = timeOf(expiresAt)
		return lease, err
	})
}

func (db *DB) listFirewallGateways(ctx context.Context, id string) ([]firewall.Gateway, error) {
	rows, err := db.sql.QueryContext(ctx,
		`SELECT name, address, status, delay_ms, loss_percent FROM firewall_gateways WHERE firewall_id = ? ORDER BY name`, id)
	if err != nil {
		return nil, fmt.Errorf("list firewall gateways: %w", err)
	}
	return collect(rows, func(row scanner) (firewall.Gateway, error) {
		var gateway firewall.Gateway
		var delay, loss sql.NullFloat64
		err := row.Scan(&gateway.Name, &gateway.Address, &gateway.Status, &delay, &loss)
		gateway.IsMonitored = delay.Valid || loss.Valid
		gateway.DelayMillis, gateway.LossPercent = delay.Float64, loss.Float64
		return gateway, err
	})
}
