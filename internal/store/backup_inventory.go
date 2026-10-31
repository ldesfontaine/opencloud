package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/backup"
	"github.com/ldesfontaine/opencloud/internal/proxmox"
)

// ListObservedGuests rend les VM et conteneurs que les hôtes Proxmox ont
// relevés, modèles exclus, par hôte puis par numéro. Rien n'est demandé à
// Proxmox : c'est le dernier relevé.
func (db *DB) ListObservedGuests(ctx context.Context) ([]backup.ObservedGuest, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT g.machine_id, m.name, g.vmid, g.kind, g.name, g.node, g.backup_count, g.backup_total_bytes,
			g.last_backup_at, g.last_backup_bytes, g.last_backup_storage
		FROM proxmox_guests g JOIN machines m ON m.id = g.machine_id
		WHERE g.template = 0
		ORDER BY m.name, g.machine_id, g.vmid`)
	if err != nil {
		return nil, fmt.Errorf("list observed guests: %w", err)
	}
	return collect(rows, func(row scanner) (backup.ObservedGuest, error) {
		var guest backup.ObservedGuest
		var kind string
		var lastAt *int64
		err := row.Scan(&guest.HostMachineID, &guest.HostMachineName, &guest.VMID, &kind, &guest.Name, &guest.Node,
			&guest.Archives.Count, &guest.Archives.TotalBytes, &lastAt, &guest.Archives.LastBytes, &guest.Archives.Storage)
		guest.Kind = backup.KindVM
		if kind == proxmox.KindLXC {
			guest.Kind = backup.KindLXC
		}
		if lastAt != nil {
			guest.Archives.LastAt = time.Unix(*lastAt, 0).UTC()
		}
		return guest, err
	})
}

// ListObservedStorages rend les stockages des hôtes qui acceptent des
// sauvegardes, par hôte puis par nom.
func (db *DB) ListObservedStorages(ctx context.Context) ([]backup.ObservedStorage, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT s.machine_id, m.name, s.node, s.storage, s.type, s.total, s.used, s.avail, s.active
		FROM proxmox_storages s JOIN machines m ON m.id = s.machine_id
		WHERE ',' || s.content || ',' LIKE '%,' || ? || ',%'
		ORDER BY m.name, s.machine_id, s.storage, s.node`, proxmox.ContentBackup)
	if err != nil {
		return nil, fmt.Errorf("list observed storages: %w", err)
	}
	return collect(rows, func(row scanner) (backup.ObservedStorage, error) {
		var storage backup.ObservedStorage
		err := row.Scan(&storage.HostMachineID, &storage.HostMachineName, &storage.Node, &storage.Name, &storage.Type,
			&storage.Total, &storage.Used, &storage.Avail, &storage.Active)
		return storage, err
	})
}

// ListObservedVolumes rend les volumes Docker que les agents ont listés, par
// machine puis par nom.
func (db *DB) ListObservedVolumes(ctx context.Context) ([]backup.ObservedVolume, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT v.machine_id, m.name, v.name, v.driver, v.used_by
		FROM machine_volumes v JOIN machines m ON m.id = v.machine_id
		ORDER BY m.name, v.machine_id, v.name`)
	if err != nil {
		return nil, fmt.Errorf("list observed volumes: %w", err)
	}
	return collect(rows, func(row scanner) (backup.ObservedVolume, error) {
		var volume backup.ObservedVolume
		var usedBy string
		if err := row.Scan(&volume.MachineID, &volume.MachineName, &volume.Name, &volume.Driver, &usedBy); err != nil {
			return backup.ObservedVolume{}, err
		}
		if err := json.Unmarshal([]byte(usedBy), &volume.UsedBy); err != nil {
			return backup.ObservedVolume{}, fmt.Errorf("decode volume users: %w", err)
		}
		return volume, nil
	})
}

// LatestRunsBySource rend, par source, la dernière ligne de l'historique et la
// dernière réussie. Une ligne sans source (le plan entier refusé) n'est celle
// d'aucune.
func (db *DB) LatestRunsBySource(ctx context.Context) (backup.LatestRuns, error) {
	latest := backup.LatestRuns{Last: map[string]backup.RunRecord{}, LastOK: map[string]backup.RunRecord{}}
	for _, kind := range []struct {
		into   map[string]backup.RunRecord
		filter string
	}{{latest.Last, ""}, {latest.LastOK, " AND result = 'ok'"}} {
		rows, err := db.sql.QueryContext(ctx, `
			SELECT id, subject, pass_at, source, started_at, finished_at, result, reason, size_bytes,
				destination_id, destination_name, action_run_id
			FROM backup_runs
			WHERE source <> ''`+kind.filter+` AND id IN (
				SELECT MAX(id) FROM backup_runs WHERE source <> ''`+kind.filter+` GROUP BY source)`) // #nosec G202 -- le filtre est une constante de cette fonction.
		if err != nil {
			return backup.LatestRuns{}, fmt.Errorf("read latest backup runs: %w", err)
		}
		records, err := collect(rows, scanBackupRun)
		if err != nil {
			return backup.LatestRuns{}, fmt.Errorf("scan latest backup runs: %w", err)
		}
		for _, record := range records {
			kind.into[record.Source] = record
		}
	}
	return latest, nil
}
