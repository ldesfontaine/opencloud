package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/backup"
	"github.com/ldesfontaine/opencloud/internal/proxmox"
)

var (
	_ proxmox.Store = (*DB)(nil)
	_ backup.Guests = (*DB)(nil)
)

// contentSeparator sépare les contenus d'un stockage dans la colonne ; aucun
// nom de contenu de Proxmox n'en contient.
const contentSeparator = ","

const proxmoxHostColumns = `machine_id, api_url, token_id, token_secret, pin, node, version, verified_at, last_read_at, last_error, created_at`

// scanProxmoxHost lit l'hôte et son secret tel que la base le garde, scellé.
func scanProxmoxHost(row scanner) (host proxmox.Host, sealedSecret string, err error) {
	var verifiedAt, createdAt int64
	var lastReadAt sql.NullInt64
	err = row.Scan(&host.MachineID, &host.APIURL, &host.TokenID, &sealedSecret, &host.Pin, &host.Node, &host.Version,
		&verifiedAt, &lastReadAt, &host.LastError, &createdAt)
	host.VerifiedAt = time.Unix(verifiedAt, 0)
	host.LastReadAt = timeOf(lastReadAt)
	host.CreatedAt = time.Unix(createdAt, 0)
	return host, sealedSecret, err
}

// GetProxmoxCredentials rend l'hôte avec son secret ouvert : pour parler à
// l'API, jamais pour une réponse.
func (db *DB) GetProxmoxCredentials(ctx context.Context, machineID string) (proxmox.Credentials, error) {
	host, sealed, err := scanProxmoxHost(db.sql.QueryRowContext(ctx,
		`SELECT `+proxmoxHostColumns+` FROM machine_proxmox WHERE machine_id = ?`, machineID))
	if errors.Is(err, sql.ErrNoRows) {
		return proxmox.Credentials{}, proxmox.ErrNotFound
	}
	if err != nil {
		return proxmox.Credentials{}, fmt.Errorf("get proxmox host: %w", err)
	}
	secret, err := db.openSecret(sealed, proxmoxTokenScope(machineID))
	if err != nil {
		return proxmox.Credentials{}, fmt.Errorf("open proxmox token: %w", err)
	}
	return proxmox.Credentials{Host: host, TokenSecret: secret}, nil
}

// SaveProxmoxHost déclare l'hôte, ou remplace sa connexion. Une autre adresse
// est un autre hôte : ce que l'ancien avait relevé part, au lieu de rester
// affiché sous la nouvelle.
func (db *DB) SaveProxmoxHost(ctx context.Context, credentials proxmox.Credentials) error {
	sealed, err := db.sealSecret(credentials.TokenSecret, proxmoxTokenScope(credentials.MachineID))
	if err != nil {
		return fmt.Errorf("seal proxmox token: %w", err)
	}
	return db.inTx(ctx, "save proxmox host", func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM machine_proxmox WHERE machine_id = ? AND api_url <> ?`, credentials.MachineID, credentials.APIURL); err != nil {
			return fmt.Errorf("replace proxmox host: %w", err)
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO machine_proxmox (machine_id, api_url, token_id, token_secret, pin, version, verified_at, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (machine_id) DO UPDATE SET token_id = excluded.token_id, token_secret = excluded.token_secret,
				pin = excluded.pin, version = excluded.version, verified_at = excluded.verified_at, last_error = ''`,
			credentials.MachineID, credentials.APIURL, credentials.TokenID, sealed, credentials.Pin, credentials.Version,
			credentials.VerifiedAt.Unix(), credentials.CreatedAt.Unix())
		if err != nil {
			return fmt.Errorf("upsert proxmox host: %w", err)
		}
		return nil
	})
}

// DeleteProxmoxHost retire l'hôte ; ses VM et ses stockages partent avec lui.
func (db *DB) DeleteProxmoxHost(ctx context.Context, machineID string) error {
	result, err := db.sql.ExecContext(ctx, `DELETE FROM machine_proxmox WHERE machine_id = ?`, machineID)
	if err != nil {
		return fmt.Errorf("delete proxmox host: %w", err)
	}
	return notFoundIfNoRow(result, proxmox.ErrNotFound)
}

func (db *DB) ListProxmoxHostIDs(ctx context.Context) ([]string, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT machine_id FROM machine_proxmox ORDER BY machine_id`)
	if err != nil {
		return nil, fmt.Errorf("list proxmox hosts: %w", err)
	}
	return collect(rows, func(row scanner) (string, error) {
		var machineID string
		err := row.Scan(&machineID)
		return machineID, err
	})
}

// NoteProxmoxFailure ne touche qu'à la clé du refus : le dernier relevé
// réussi reste ce que la fiche montre.
func (db *DB) NoteProxmoxFailure(ctx context.Context, machineID, errorKey string) error {
	result, err := db.sql.ExecContext(ctx, `UPDATE machine_proxmox SET last_error = ? WHERE machine_id = ?`, errorKey, machineID)
	if err != nil {
		return fmt.Errorf("note proxmox failure: %w", err)
	}
	return notFoundIfNoRow(result, proxmox.ErrNotFound)
}

// ReplaceProxmoxReading remplace en une transaction tout ce que l'hôte avait
// relevé : ce qu'un relevé ne montre plus disparaît.
func (db *DB) ReplaceProxmoxReading(ctx context.Context, machineID string, reading proxmox.Reading, at time.Time) error {
	return db.inTx(ctx, "replace proxmox reading", func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
			UPDATE machine_proxmox SET node = ?, version = ?, last_read_at = ?, last_error = '' WHERE machine_id = ?`,
			reading.Node, reading.Version, at.Unix(), machineID)
		if err != nil {
			return fmt.Errorf("update proxmox host: %w", err)
		}
		if err := notFoundIfNoRow(result, proxmox.ErrNotFound); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM proxmox_guests WHERE machine_id = ?`, machineID); err != nil {
			return fmt.Errorf("delete proxmox guests: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM proxmox_storages WHERE machine_id = ?`, machineID); err != nil {
			return fmt.Errorf("delete proxmox storages: %w", err)
		}
		for _, guest := range reading.Guests {
			if err := insertProxmoxGuest(ctx, tx, machineID, guest); err != nil {
				return err
			}
		}
		for _, storage := range reading.Storages {
			if err := insertProxmoxStorage(ctx, tx, machineID, storage); err != nil {
				return err
			}
		}
		return nil
	})
}

func insertProxmoxGuest(ctx context.Context, tx *sql.Tx, machineID string, guest proxmox.Guest) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO proxmox_guests (machine_id, vmid, kind, name, status, template, cpus, maxmem, maxdisk, node,
			linked_machine_id, last_backup_at, last_backup_bytes, last_backup_storage, backup_count, backup_total_bytes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		machineID, guest.VMID, guest.Kind, guest.Name, guest.Status, guest.Template, guest.CPUs, guest.MaxMem, guest.MaxDisk,
		guest.Node, nullableString(guest.LinkedMachineID), nullableTime(guest.LastBackupAt), guest.LastBackupBytes,
		guest.LastBackupStorage, guest.BackupCount, guest.BackupTotalBytes)
	if err != nil {
		return fmt.Errorf("insert proxmox guest %d: %w", guest.VMID, err)
	}
	return nil
}

func insertProxmoxStorage(ctx context.Context, tx *sql.Tx, machineID string, storage proxmox.Storage) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO proxmox_storages (machine_id, node, storage, type, total, used, avail, content, active)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		machineID, storage.Node, storage.Name, storage.Type, storage.Total, storage.Used, storage.Avail,
		strings.Join(storage.Content, contentSeparator), storage.Active)
	if err != nil {
		return fmt.Errorf("insert proxmox storage %s: %w", storage.Name, err)
	}
	return nil
}

const proxmoxGuestColumns = `g.vmid, g.kind, g.name, g.status, g.template, g.cpus, g.maxmem, g.maxdisk, g.node,
	g.linked_machine_id, g.last_backup_at, g.last_backup_bytes, g.last_backup_storage, g.backup_count, g.backup_total_bytes`

// guestRow lit les colonnes de proxmoxGuestColumns ; une requête qui en ajoute
// d'autres les scanne après destinations().
type guestRow struct {
	guest        proxmox.Guest
	linked       sql.NullString
	lastBackupAt sql.NullInt64
}

func (row *guestRow) destinations() []any {
	guest := &row.guest
	return []any{&guest.VMID, &guest.Kind, &guest.Name, &guest.Status, &guest.Template, &guest.CPUs, &guest.MaxMem,
		&guest.MaxDisk, &guest.Node, &row.linked, &row.lastBackupAt, &guest.LastBackupBytes, &guest.LastBackupStorage,
		&guest.BackupCount, &guest.BackupTotalBytes}
}

func (row *guestRow) result() proxmox.Guest {
	guest := row.guest
	guest.LinkedMachineID = row.linked.String
	guest.LastBackupAt = timeOf(row.lastBackupAt)
	return guest
}

func scanProxmoxGuest(row scanner) (proxmox.Guest, error) {
	var read guestRow
	if err := row.Scan(read.destinations()...); err != nil {
		return proxmox.Guest{}, err
	}
	return read.result(), nil
}

// GetProxmoxView rend l'hôte d'une machine avec ses VM, par numéro, et ses
// stockages, par nom : ce que la fiche montre, jamais le secret.
func (db *DB) GetProxmoxView(ctx context.Context, machineID string) (proxmox.View, error) {
	host, _, err := scanProxmoxHost(db.sql.QueryRowContext(ctx,
		`SELECT `+proxmoxHostColumns+` FROM machine_proxmox WHERE machine_id = ?`, machineID))
	if errors.Is(err, sql.ErrNoRows) {
		return proxmox.View{}, proxmox.ErrNotFound
	}
	if err != nil {
		return proxmox.View{}, fmt.Errorf("get proxmox host: %w", err)
	}
	rows, err := db.sql.QueryContext(ctx,
		`SELECT `+proxmoxGuestColumns+` FROM proxmox_guests g WHERE g.machine_id = ? ORDER BY g.vmid`, machineID)
	if err != nil {
		return proxmox.View{}, fmt.Errorf("list proxmox guests: %w", err)
	}
	guests, err := collect(rows, scanProxmoxGuest)
	if err != nil {
		return proxmox.View{}, fmt.Errorf("scan proxmox guests: %w", err)
	}
	rows, err = db.sql.QueryContext(ctx, `
		SELECT node, storage, type, total, used, avail, content, active
		FROM proxmox_storages WHERE machine_id = ? ORDER BY storage, node`, machineID)
	if err != nil {
		return proxmox.View{}, fmt.Errorf("list proxmox storages: %w", err)
	}
	storages, err := collect(rows, scanProxmoxStorage)
	if err != nil {
		return proxmox.View{}, fmt.Errorf("scan proxmox storages: %w", err)
	}
	return proxmox.View{Host: host, Guests: guests, Storages: storages}, nil
}

// ListProxmoxGuestIDs rend les numéros des VM et conteneurs que l'hôte de cette
// machine a relevés, modèles exclus (vzdump n'en sauvegarde pas) ; rien si la
// machine ne porte pas d'hôte ou si aucun relevé n'a eu lieu.
func (db *DB) ListProxmoxGuestIDs(ctx context.Context, machineID string) ([]int, error) {
	rows, err := db.sql.QueryContext(ctx,
		`SELECT vmid FROM proxmox_guests WHERE machine_id = ? AND template = 0 ORDER BY vmid`, machineID)
	if err != nil {
		return nil, fmt.Errorf("list proxmox guest ids: %w", err)
	}
	return collect(rows, func(row scanner) (int, error) {
		var vmid int
		err := row.Scan(&vmid)
		return vmid, err
	})
}

func scanProxmoxStorage(row scanner) (proxmox.Storage, error) {
	var storage proxmox.Storage
	var content string
	err := row.Scan(&storage.Node, &storage.Name, &storage.Type, &storage.Total, &storage.Used, &storage.Avail, &content, &storage.Active)
	storage.Content = []string{}
	if content != "" {
		storage.Content = strings.Split(content, contentSeparator)
	}
	return storage, err
}

// ProxmoxSummaries dit, pour chaque machine qui touche à Proxmox, ce qu'elle
// porte (un hôte et ses VM) ou ce qu'elle est (la VM d'un hôte). Une machine
// que rien ne lie n'y figure pas. Deux VM de deux hôtes qui portent le même
// nom la lient toutes deux : la première, par nom d'hôte, la décrit.
func (db *DB) ProxmoxSummaries(ctx context.Context) (map[string]proxmox.Summary, error) {
	summaries := map[string]proxmox.Summary{}
	if err := db.readDeclaredHosts(ctx, summaries); err != nil {
		return nil, err
	}
	if err := db.readHostedGuests(ctx, summaries); err != nil {
		return nil, err
	}
	return summaries, nil
}

func (db *DB) readDeclaredHosts(ctx context.Context, into map[string]proxmox.Summary) error {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT h.machine_id, h.last_error, COUNT(g.vmid)
		FROM machine_proxmox h LEFT JOIN proxmox_guests g ON g.machine_id = h.machine_id
		GROUP BY h.machine_id, h.last_error`)
	if err != nil {
		return fmt.Errorf("count proxmox guests: %w", err)
	}
	type declared struct {
		machineID string
		summary   proxmox.Summary
	}
	hosts, err := collect(rows, func(row scanner) (declared, error) {
		host := declared{summary: proxmox.Summary{Declared: true}}
		err := row.Scan(&host.machineID, &host.summary.LastError, &host.summary.Guests)
		return host, err
	})
	if err != nil {
		return fmt.Errorf("scan proxmox hosts: %w", err)
	}
	for _, host := range hosts {
		into[host.machineID] = host.summary
	}
	return nil
}

func (db *DB) readHostedGuests(ctx context.Context, into map[string]proxmox.Summary) error {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT `+proxmoxGuestColumns+`, g.machine_id, hm.name
		FROM proxmox_guests g JOIN machines hm ON hm.id = g.machine_id
		WHERE g.linked_machine_id IS NOT NULL
		ORDER BY hm.name, g.vmid`)
	if err != nil {
		return fmt.Errorf("list hosted proxmox guests: %w", err)
	}
	hosted, err := collect(rows, func(row scanner) (proxmox.HostedGuest, error) {
		var read guestRow
		var entry proxmox.HostedGuest
		if err := row.Scan(append(read.destinations(), &entry.HostMachineID, &entry.HostName)...); err != nil {
			return proxmox.HostedGuest{}, err
		}
		entry.Guest = read.result()
		return entry, nil
	})
	if err != nil {
		return fmt.Errorf("scan hosted proxmox guests: %w", err)
	}
	for _, entry := range hosted {
		summary := into[entry.LinkedMachineID]
		if summary.Hosted == nil {
			summary.Hosted = &entry
			into[entry.LinkedMachineID] = summary
		}
	}
	return nil
}
