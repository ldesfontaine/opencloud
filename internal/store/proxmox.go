package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/backup"
	"github.com/ldesfontaine/opencloud/internal/ident"
	"github.com/ldesfontaine/opencloud/internal/proxmox"
)

var (
	_ proxmox.Store = (*DB)(nil)
	_ backup.Guests = (*DB)(nil)
)

// contentSeparator sépare les contenus d'un stockage dans la colonne ; aucun
// nom de contenu de Proxmox n'en contient.
const contentSeparator = ","

// blindStorageExists dit, pour une ligne de proxmox_guests aliasée « g », que l'hôte
// a un stockage dont le jeton ne lit pas les archives : sans archive connue, la
// VM n'est pas « sans sauvegarde », on ne sait pas.
const blindStorageExists = `EXISTS (SELECT 1 FROM proxmox_storages s WHERE s.machine_id = g.machine_id AND s.backups_unreadable = 1)`

const proxmoxHostColumns = `machine_id, api_url, token_id, token_secret, pin, node, version, verified_at, last_read_at, last_error,
	backup_jobs_unreadable, created_at`

// scanProxmoxHost lit l'hôte et son secret tel que la base le garde, scellé.
func scanProxmoxHost(row scanner) (host proxmox.Host, sealedSecret string, err error) {
	var verifiedAt, createdAt int64
	var lastReadAt sql.NullInt64
	err = row.Scan(&host.MachineID, &host.APIURL, &host.TokenID, &sealedSecret, &host.Pin, &host.Node, &host.Version,
		&verifiedAt, &lastReadAt, &host.LastError, &host.JobsUnreadable, &createdAt)
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
	return db.inTx(ctx, "delete proxmox host", func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM machine_proxmox WHERE machine_id = ?`, machineID)
		if err != nil {
			return fmt.Errorf("delete proxmox host: %w", err)
		}
		if err := notFoundIfNoRow(result, proxmox.ErrNotFound); err != nil {
			return err
		}
		return pruneClusters(ctx, tx)
	})
}

// ensureCluster rend la grappe de ce nom, créée au premier hôte qui la dit ;
// nulle pour un nœud seul. Deux hôtes d'une même grappe en partagent la ligne.
func ensureCluster(ctx context.Context, tx *sql.Tx, name string) (sql.NullString, error) {
	if name == "" {
		return sql.NullString{}, nil
	}
	id, err := ident.New()
	if err != nil {
		return sql.NullString{}, fmt.Errorf("new cluster id: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO hypervisor_clusters (id, kind, name) VALUES (?, 'proxmox', ?)`, id, name); err != nil {
		return sql.NullString{}, fmt.Errorf("insert cluster: %w", err)
	}
	var found string
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM hypervisor_clusters WHERE kind = 'proxmox' AND name = ?`, name).Scan(&found); err != nil {
		return sql.NullString{}, fmt.Errorf("read cluster: %w", err)
	}
	return sql.NullString{String: found, Valid: true}, nil
}

// pruneClusters retire les grappes qu'aucun hôte ne déclare plus.
func pruneClusters(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
		DELETE FROM hypervisor_clusters WHERE id NOT IN (SELECT cluster_id FROM machine_proxmox WHERE cluster_id IS NOT NULL)`)
	if err != nil {
		return fmt.Errorf("prune clusters: %w", err)
	}
	return nil
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
		clusterID, err := ensureCluster(ctx, tx, reading.Cluster)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE machine_proxmox SET node = ?, version = ?, cluster_id = ?, last_read_at = ?, last_error = '', backup_jobs_unreadable = ?
			WHERE machine_id = ?`,
			reading.Node, reading.Version, clusterID, at.Unix(), reading.JobsUnreadable, machineID)
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
		if err := replaceProxmoxBackupJobs(ctx, tx, machineID, reading.Jobs); err != nil {
			return err
		}
		return pruneClusters(ctx, tx)
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
		INSERT INTO proxmox_storages (machine_id, node, storage, type, total, used, avail, content, active, backups_unreadable)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		machineID, storage.Node, storage.Name, storage.Type, storage.Total, storage.Used, storage.Avail,
		strings.Join(storage.Content, contentSeparator), storage.Active, storage.BackupsUnreadable)
	if err != nil {
		return fmt.Errorf("insert proxmox storage %s: %w", storage.Name, err)
	}
	return nil
}

const proxmoxGuestColumns = `g.vmid, g.kind, g.name, g.status, g.template, g.cpus, g.maxmem, g.maxdisk, g.node,
	g.linked_machine_id, g.last_backup_at, g.last_backup_bytes, g.last_backup_storage, g.backup_count, g.backup_total_bytes,
	(g.backup_count = 0 AND ` + blindStorageExists + `)`

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
		&guest.BackupCount, &guest.BackupTotalBytes, &guest.BackupUnknown}
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
		SELECT node, storage, type, total, used, avail, content, active, backups_unreadable
		FROM proxmox_storages WHERE machine_id = ? ORDER BY storage, node`, machineID)
	if err != nil {
		return proxmox.View{}, fmt.Errorf("list proxmox storages: %w", err)
	}
	storages, err := collect(rows, scanProxmoxStorage)
	if err != nil {
		return proxmox.View{}, fmt.Errorf("scan proxmox storages: %w", err)
	}
	host.BlindStorages = proxmox.BlindStorages(storages)
	jobs, err := db.listProxmoxBackupJobs(ctx, machineID)
	if err != nil {
		return proxmox.View{}, err
	}
	return proxmox.View{Host: host, Guests: guests, Storages: storages, Jobs: jobs}, nil
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
	err := row.Scan(&storage.Node, &storage.Name, &storage.Type, &storage.Total, &storage.Used, &storage.Avail, &content, &storage.Active, &storage.BackupsUnreadable)
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
		SELECT h.machine_id, h.last_error, h.node, c.id, c.name, COUNT(g.vmid),
			EXISTS (SELECT 1 FROM proxmox_storages s WHERE s.machine_id = h.machine_id AND s.backups_unreadable = 1)
		FROM machine_proxmox h
		LEFT JOIN proxmox_guests g ON g.machine_id = h.machine_id
		LEFT JOIN hypervisor_clusters c ON c.id = h.cluster_id
		GROUP BY h.machine_id, h.last_error, h.node, c.id, c.name`)
	if err != nil {
		return fmt.Errorf("count proxmox guests: %w", err)
	}
	type declared struct {
		machineID string
		summary   proxmox.Summary
	}
	hosts, err := collect(rows, func(row scanner) (declared, error) {
		host := declared{summary: proxmox.Summary{Declared: true, Members: []proxmox.Guest{}}}
		var clusterID, clusterName sql.NullString
		err := row.Scan(&host.machineID, &host.summary.LastError, &host.summary.Node, &clusterID, &clusterName, &host.summary.Guests, &host.summary.BackupBlind)
		if clusterID.Valid {
			host.summary.Cluster = &proxmox.Cluster{ID: clusterID.String, Name: clusterName.String}
		}
		return host, err
	})
	if err != nil {
		return fmt.Errorf("scan proxmox hosts: %w", err)
	}
	for _, host := range hosts {
		into[host.machineID] = host.summary
	}
	return db.readMembers(ctx, into)
}

// readMembers pose sur chaque hôte déclaré les invités de son dernier relevé.
func (db *DB) readMembers(ctx context.Context, into map[string]proxmox.Summary) error {
	rows, err := db.sql.QueryContext(ctx,
		`SELECT `+proxmoxGuestColumns+`, g.machine_id FROM proxmox_guests g ORDER BY g.machine_id, g.vmid`)
	if err != nil {
		return fmt.Errorf("list proxmox members: %w", err)
	}
	type member struct {
		hostID string
		guest  proxmox.Guest
	}
	members, err := collect(rows, func(row scanner) (member, error) {
		var read guestRow
		var entry member
		if err := row.Scan(append(read.destinations(), &entry.hostID)...); err != nil {
			return member{}, err
		}
		entry.guest = read.result()
		return entry, nil
	})
	if err != nil {
		return fmt.Errorf("scan proxmox members: %w", err)
	}
	for _, entry := range members {
		summary := into[entry.hostID]
		summary.Members = append(summary.Members, entry.guest)
		into[entry.hostID] = summary
	}
	return nil
}

func (db *DB) readHostedGuests(ctx context.Context, into map[string]proxmox.Summary) error {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT `+proxmoxGuestColumns+`, g.machine_id, hm.name
		FROM proxmox_guests g
		JOIN machines hm ON hm.id = g.machine_id
		JOIN machines lm ON lm.id = g.linked_machine_id
		WHERE lm.host_override IS NULL OR lm.host_override = g.machine_id
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
