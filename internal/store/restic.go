package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/proxmox"
	"github.com/ldesfontaine/opencloud/internal/restic"
)

var (
	_ restic.Store = (*DB)(nil)
	_ restic.Links = (*DB)(nil)
)

// MaxResticRunsPerBackup borne l'historique d'une sauvegarde restic.
const MaxResticRunsPerBackup = 60

const resticTargetColumns = `t.machine_id, m.name, t.requested_host, t.requested_port, t.requested_dir, t.requested_at,
	t.setup_run_id, t.state, t.reason, t.version, t.host, t.port, t.certificate, t.fingerprint, t.repo_bytes,
	t.disk_total, t.disk_free, t.floor_known, t.floor_daily, t.floor_weekly, t.floor_monthly, t.reported_at`

func scanResticTarget(row scanner) (restic.TargetRecord, error) {
	var record restic.TargetRecord
	var requestedAt int64
	var reportedAt sql.NullInt64
	var isFloorKnown bool
	var floor agentwire.ResticRetentionFloor
	reported := &record.Reported
	err := row.Scan(&record.MachineID, &record.MachineName, &record.Requested.Host, &record.Requested.Port, &record.Requested.Dir,
		&requestedAt, &record.SetupRunID, &reported.State, &reported.Reason, &reported.Version, &reported.Host, &reported.Port,
		&reported.Certificate, &reported.Fingerprint, &reported.RepoBytes, &reported.DiskTotal, &reported.DiskFree,
		&isFloorKnown, &floor.KeepDaily, &floor.KeepWeekly, &floor.KeepMonthly, &reportedAt)
	record.RequestedAt = time.Unix(requestedAt, 0)
	record.ReportedAt = timeOf(reportedAt)
	if isFloorKnown {
		reported.RetentionFloor = &floor
	}
	return record, err
}

func (db *DB) GetResticTarget(ctx context.Context, machineID string) (restic.TargetRecord, error) {
	record, err := scanResticTarget(db.sql.QueryRowContext(ctx, `
		SELECT `+resticTargetColumns+` FROM restic_targets t JOIN machines m ON m.id = t.machine_id
		WHERE t.machine_id = ?`, machineID))
	if errors.Is(err, sql.ErrNoRows) {
		return restic.TargetRecord{}, restic.ErrTargetNotFound
	}
	if err != nil {
		return restic.TargetRecord{}, fmt.Errorf("get restic target: %w", err)
	}
	return record, nil
}

func (db *DB) ListResticTargets(ctx context.Context) ([]restic.TargetRecord, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT `+resticTargetColumns+` FROM restic_targets t JOIN machines m ON m.id = t.machine_id
		ORDER BY m.name, t.machine_id`)
	if err != nil {
		return nil, fmt.Errorf("list restic targets: %w", err)
	}
	return collect(rows, scanResticTarget)
}

// SaveResticTargetRequest garde ce que l'agent a rapporté : une nouvelle
// demande ne l'efface pas, le prochain rapport le remplace.
func (db *DB) SaveResticTargetRequest(ctx context.Context, request restic.TargetRecord) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO restic_targets (machine_id, requested_host, requested_port, requested_dir, requested_at, setup_run_id)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (machine_id) DO UPDATE SET requested_host = excluded.requested_host,
			requested_port = excluded.requested_port, requested_dir = excluded.requested_dir,
			requested_at = excluded.requested_at, setup_run_id = excluded.setup_run_id`,
		request.MachineID, request.Requested.Host, request.Requested.Port, request.Requested.Dir,
		request.RequestedAt.Unix(), request.SetupRunID)
	if err != nil {
		return fmt.Errorf("save restic target: %w", err)
	}
	return nil
}

func (db *DB) DeleteResticTarget(ctx context.Context, machineID string) error {
	result, err := db.sql.ExecContext(ctx, `DELETE FROM restic_targets WHERE machine_id = ?`, machineID)
	if err != nil {
		return fmt.Errorf("delete restic target: %w", err)
	}
	return notFoundIfNoRow(result, restic.ErrTargetNotFound)
}

// RecordResticTargetReport n'écrit que pour une cible déclarée : un agent qui
// a le droit sans que l'opérateur l'ait posé n'entre pas en base. Les noms
// d'utilisateurs ne s'écrivent pas : ils ne servent qu'à rejouer un lien.
func (db *DB) RecordResticTargetReport(ctx context.Context, machineID string, report agentwire.ResticTargetReport, at time.Time) (bool, error) {
	var isFloorKnown bool
	var floor agentwire.ResticRetentionFloor
	if report.RetentionFloor != nil {
		isFloorKnown, floor = true, *report.RetentionFloor
	}
	result, err := db.sql.ExecContext(ctx, `
		UPDATE restic_targets SET state = ?, reason = ?, version = ?, host = ?, port = ?, certificate = ?, fingerprint = ?,
			repo_bytes = ?, disk_total = ?, disk_free = ?, floor_known = ?, floor_daily = ?, floor_weekly = ?, floor_monthly = ?,
			reported_at = ?
		WHERE machine_id = ?`,
		report.State, report.Reason, report.Version, report.Host, report.Port, report.Certificate, report.Fingerprint,
		report.RepoBytes, report.DiskTotal, report.DiskFree, isFloorKnown, floor.KeepDaily, floor.KeepWeekly, floor.KeepMonthly,
		at.Unix(), machineID)
	if err != nil {
		return false, fmt.Errorf("record restic target report: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

const resticBackupColumns = `b.id, b.name, b.source_machine_id, sm.name, b.scope, b.vmid, b.guest_type,
	COALESCE(b.service_id, ''), COALESCE(ds.name, ''), b.target_machine_id, tm.name,
	b.repo_user, b.schedule, b.keep_last, b.keep_daily, b.keep_weekly, b.keep_monthly,
	b.keep_yearly, b.created_at, b.last_attempt_at, b.last_run_id, b.last_state, b.last_reason, b.last_path,
	b.last_path_reason, b.last_target_host, b.last_target_port, b.last_snapshot, b.last_bytes_added, b.last_duration,
	b.last_parts, b.last_finished_at, b.last_success_at`

const resticBackupFrom = `
	FROM restic_backups b
	JOIN machines sm ON sm.id = b.source_machine_id
	JOIN machines tm ON tm.id = b.target_machine_id
	LEFT JOIN declared_services ds ON ds.id = b.service_id`

func scanResticBackup(row scanner) (restic.Backup, error) {
	var found restic.Backup
	var createdAt int64
	var lastAttempt, lastFinished, lastSuccess sql.NullInt64
	var lastParts string
	retention := &found.Retention
	report := &found.Last.Report
	err := row.Scan(&found.ID, &found.Name, &found.SourceMachineID, &found.SourceMachineName, &found.Scope, &found.VMID,
		&found.GuestType, &found.ServiceID, &found.ServiceName, &found.TargetMachineID, &found.TargetMachineName,
		&found.RepoUser, &found.Schedule, &retention.Last, &retention.Daily, &retention.Weekly,
		&retention.Monthly, &retention.Yearly, &createdAt, &lastAttempt, &found.LastRunID, &found.Last.State, &found.Last.Reason,
		&report.Path, &report.Reason, &report.TargetHost, &report.TargetPort, &report.SnapshotID, &report.BytesAdded,
		&report.DurationSeconds, &lastParts, &lastFinished, &lastSuccess)
	if err != nil {
		return found, err
	}
	if report.Parts, err = decodeResticParts(lastParts); err != nil {
		return found, err
	}
	found.CreatedAt = time.Unix(createdAt, 0)
	found.LastAttemptAt = timeOf(lastAttempt)
	found.Last.RunID = found.LastRunID
	found.Last.FinishedAt = timeOf(lastFinished)
	found.LastSuccessAt = timeOf(lastSuccess)
	return found, nil
}

// encodeResticParts écrit les parties d'un rapport déjà rejugé (Validate) ;
// aucune : « [] ».
func encodeResticParts(parts []agentwire.ResticPartReport) (string, error) {
	encoded, err := encodeList(parts)
	if err != nil {
		return "", fmt.Errorf("encode restic parts: %w", err)
	}
	return encoded, nil
}

func decodeResticParts(encoded string) ([]agentwire.ResticPartReport, error) {
	parts := []agentwire.ResticPartReport{}
	if err := json.Unmarshal([]byte(encoded), &parts); err != nil {
		return nil, fmt.Errorf("decode restic parts: %w", err)
	}
	return parts, nil
}

func (db *DB) GetResticBackup(ctx context.Context, id string) (restic.Backup, error) {
	found, err := scanResticBackup(db.sql.QueryRowContext(ctx, `SELECT `+resticBackupColumns+resticBackupFrom+` WHERE b.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return restic.Backup{}, restic.ErrBackupNotFound
	}
	if err != nil {
		return restic.Backup{}, fmt.Errorf("get restic backup: %w", err)
	}
	return found, nil
}

func (db *DB) ListResticBackups(ctx context.Context) ([]restic.Backup, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+resticBackupColumns+resticBackupFrom+` ORDER BY b.name, b.id`)
	if err != nil {
		return nil, fmt.Errorf("list restic backups: %w", err)
	}
	return collect(rows, scanResticBackup)
}

func (db *DB) FindResticBackupByRun(ctx context.Context, sourceID, runID string) (restic.Backup, error) {
	if runID == "" {
		return restic.Backup{}, restic.ErrBackupNotFound
	}
	found, err := scanResticBackup(db.sql.QueryRowContext(ctx,
		`SELECT `+resticBackupColumns+resticBackupFrom+` WHERE b.last_run_id = ? AND b.source_machine_id = ?`, runID, sourceID))
	if errors.Is(err, sql.ErrNoRows) {
		return restic.Backup{}, restic.ErrBackupNotFound
	}
	if err != nil {
		return restic.Backup{}, fmt.Errorf("find restic backup by run: %w", err)
	}
	return found, nil
}

func (db *DB) InsertResticBackup(ctx context.Context, created restic.Backup) error {
	retention := created.Retention
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO restic_backups (id, name, source_machine_id, scope, vmid, guest_type, service_id, target_machine_id,
			repo_user, schedule, keep_last, keep_daily, keep_weekly, keep_monthly, keep_yearly, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		created.ID, created.Name, created.SourceMachineID, created.Scope, created.VMID, created.GuestType,
		nullableString(created.ServiceID), created.TargetMachineID, created.RepoUser, created.Schedule,
		retention.Last, retention.Daily, retention.Weekly, retention.Monthly, retention.Yearly, created.CreatedAt.Unix())
	if isUniqueViolation(err) {
		return restic.ErrBackupExists
	}
	if err != nil {
		return fmt.Errorf("insert restic backup: %w", err)
	}
	return nil
}

func (db *DB) UpdateResticBackup(ctx context.Context, changed restic.Backup) error {
	retention := changed.Retention
	result, err := db.sql.ExecContext(ctx, `
		UPDATE restic_backups SET name = ?, keep_last = ?, keep_daily = ?, keep_weekly = ?, keep_monthly = ?, keep_yearly = ?
		WHERE id = ?`,
		changed.Name, retention.Last, retention.Daily, retention.Weekly, retention.Monthly, retention.Yearly, changed.ID)
	if isUniqueViolation(err) {
		return restic.ErrBackupExists
	}
	if err != nil {
		return fmt.Errorf("update restic backup: %w", err)
	}
	return notFoundIfNoRow(result, restic.ErrBackupNotFound)
}

func (db *DB) DeleteResticBackup(ctx context.Context, id string) error {
	result, err := db.sql.ExecContext(ctx, `DELETE FROM restic_backups WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete restic backup: %w", err)
	}
	return notFoundIfNoRow(result, restic.ErrBackupNotFound)
}

// NoteResticRunSent pose le nouvel ordre ; l'état de la dernière exécution
// repart de « envoyé », son rapport de chemin reste jusqu'au suivant.
func (db *DB) NoteResticRunSent(ctx context.Context, id, runID string, at time.Time) error {
	result, err := db.sql.ExecContext(ctx, `
		UPDATE restic_backups SET last_attempt_at = ?, last_run_id = ?, last_state = ?, last_reason = '' WHERE id = ?`,
		at.Unix(), runID, restic.StateSent, id)
	if err != nil {
		return fmt.Errorf("note restic run: %w", err)
	}
	return notFoundIfNoRow(result, restic.ErrBackupNotFound)
}

// RecordResticOutcome écrit l'état du dernier ordre, et son rapport s'il en a
// un ; fini, une ligne d'historique, et la purge au-delà de la borne.
func (db *DB) RecordResticOutcome(ctx context.Context, id string, outcome restic.Outcome) error {
	return db.inTx(ctx, "record restic outcome", func(tx *sql.Tx) error {
		if err := updateResticLast(ctx, tx, id, outcome); err != nil {
			return err
		}
		if !outcome.IsFinished() {
			return nil
		}
		report := outcome.Report
		parts, err := encodeResticParts(report.Parts)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO restic_runs (backup_id, run_id, finished_at, state, reason, path, path_reason, bytes_added, duration_seconds, parts)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, outcome.RunID, outcome.FinishedAt.Unix(), outcome.State, outcome.Reason, report.Path, report.Reason,
			report.BytesAdded, report.DurationSeconds, parts); err != nil {
			return fmt.Errorf("insert restic run: %w", err)
		}
		_, err = tx.ExecContext(ctx, `
			DELETE FROM restic_runs WHERE backup_id = ? AND id NOT IN (
				SELECT id FROM restic_runs WHERE backup_id = ? ORDER BY id DESC LIMIT ?)`, id, id, MaxResticRunsPerBackup)
		if err != nil {
			return fmt.Errorf("prune restic runs: %w", err)
		}
		return nil
	})
}

func updateResticLast(ctx context.Context, tx *sql.Tx, id string, outcome restic.Outcome) error {
	if outcome.Report.Path == "" {
		result, err := tx.ExecContext(ctx, `
			UPDATE restic_backups SET last_state = ?, last_reason = ?, last_finished_at = COALESCE(?, last_finished_at) WHERE id = ?`,
			outcome.State, outcome.Reason, nullableTime(outcome.FinishedAt), id)
		if err != nil {
			return fmt.Errorf("update restic backup state: %w", err)
		}
		return notFoundIfNoRow(result, restic.ErrBackupNotFound)
	}
	report := outcome.Report
	var success any
	if outcome.State == restic.StateSucceeded {
		success = nullableTime(outcome.FinishedAt)
	}
	parts, err := encodeResticParts(report.Parts)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE restic_backups SET last_state = ?, last_reason = ?, last_path = ?, last_path_reason = ?, last_target_host = ?,
			last_target_port = ?, last_snapshot = ?, last_bytes_added = ?, last_duration = ?, last_parts = ?,
			last_finished_at = COALESCE(?, last_finished_at), last_success_at = COALESCE(?, last_success_at)
		WHERE id = ?`,
		outcome.State, outcome.Reason, report.Path, report.Reason, report.TargetHost, report.TargetPort, report.SnapshotID,
		report.BytesAdded, report.DurationSeconds, parts, nullableTime(outcome.FinishedAt), success, id)
	if err != nil {
		return fmt.Errorf("update restic backup outcome: %w", err)
	}
	return notFoundIfNoRow(result, restic.ErrBackupNotFound)
}

// ListRecentResticRuns rend, par sauvegarde, ses dernières exécutions, les
// plus récentes d'abord, en une requête.
func (db *DB) ListRecentResticRuns(ctx context.Context, perBackup int) (map[string][]restic.RunRecord, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT backup_id, run_id, finished_at, state, reason, path, path_reason, bytes_added, duration_seconds, parts
		FROM (SELECT *, ROW_NUMBER() OVER (PARTITION BY backup_id ORDER BY id DESC) AS position FROM restic_runs)
		WHERE position <= ? ORDER BY backup_id, id DESC`, perBackup)
	if err != nil {
		return nil, fmt.Errorf("list restic runs: %w", err)
	}
	type owned struct {
		backupID string
		record   restic.RunRecord
	}
	records, err := collect(rows, func(row scanner) (owned, error) {
		var entry owned
		var finishedAt int64
		var parts string
		record := &entry.record
		err := row.Scan(&entry.backupID, &record.RunID, &finishedAt, &record.State, &record.Reason, &record.Path,
			&record.PathReason, &record.BytesAdded, &record.DurationSeconds, &parts)
		if err != nil {
			return entry, err
		}
		record.FinishedAt = time.Unix(finishedAt, 0)
		reports, err := decodeResticParts(parts)
		record.Parts = restic.PartResults(reports)
		return entry, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan restic runs: %w", err)
	}
	byBackup := map[string][]restic.RunRecord{}
	for _, entry := range records {
		byBackup[entry.backupID] = append(byBackup[entry.backupID], entry.record)
	}
	return byBackup, nil
}

// RelayRepo rend le dépôt d'une source sur une cible, d'après les sauvegardes
// déclarées : sans sauvegarde de cette source vers cette cible, rien ne passe
// le relais (restic.ErrNotLinked).
func (db *DB) RelayRepo(ctx context.Context, sourceID, targetID string) (string, error) {
	var repo string
	err := db.sql.QueryRowContext(ctx, `
		SELECT repo_user FROM restic_backups WHERE source_machine_id = ? AND target_machine_id = ? LIMIT 1`,
		sourceID, targetID).Scan(&repo)
	if errors.Is(err, sql.ErrNoRows) {
		return "", restic.ErrNotLinked
	}
	if err != nil {
		return "", fmt.Errorf("read relay repo: %w", err)
	}
	return repo, nil
}

// ResticGuest rend un invité que l'hôte de la machine a relevé, modèle exclu.
func (db *DB) ResticGuest(ctx context.Context, hostID string, vmid int) (proxmox.Guest, error) {
	var guest proxmox.Guest
	err := db.sql.QueryRowContext(ctx, `
		SELECT vmid, kind, name, node FROM proxmox_guests WHERE machine_id = ? AND vmid = ? AND template = 0`,
		hostID, vmid).Scan(&guest.VMID, &guest.Kind, &guest.Name, &guest.Node)
	if errors.Is(err, sql.ErrNoRows) {
		return proxmox.Guest{}, proxmox.ErrNotFound
	}
	if err != nil {
		return proxmox.Guest{}, fmt.Errorf("get proxmox guest: %w", err)
	}
	return guest, nil
}

// resticServiceColumns : le dossier n'existe que pour un service Compose
// (Project vide sinon).
const resticServiceColumns = `s.id, s.name, s.machine_id, m.name, CASE WHEN s.origin = ? THEN s.compose_project ELSE '' END
	FROM declared_services s JOIN machines m ON m.id = s.machine_id`

func scanResticService(row scanner) (restic.ServiceRef, error) {
	var service restic.ServiceRef
	err := row.Scan(&service.ID, &service.Name, &service.MachineID, &service.MachineName, &service.Project)
	return service, err
}

// ResticService rend un service déclaré.
func (db *DB) ResticService(ctx context.Context, serviceID string) (restic.ServiceRef, error) {
	service, err := scanResticService(db.sql.QueryRowContext(ctx, `
		SELECT `+resticServiceColumns+` WHERE s.id = ?`, string(directory.OriginCompose), serviceID))
	if errors.Is(err, sql.ErrNoRows) {
		return restic.ServiceRef{}, restic.ErrServiceNotFound
	}
	if err != nil {
		return restic.ServiceRef{}, fmt.Errorf("get declared service: %w", err)
	}
	return service, nil
}

// ListResticServices rend les services déclarés, par machine puis par nom :
// le composant garde ceux qui ont quelque chose à sauvegarder.
func (db *DB) ListResticServices(ctx context.Context) ([]restic.ServiceRef, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+resticServiceColumns+`
		ORDER BY m.name, s.name, s.id`, string(directory.OriginCompose))
	if err != nil {
		return nil, fmt.Errorf("list declared services: %w", err)
	}
	return collect(rows, scanResticService)
}

// ProxmoxBackupJobsByHost rend les tâches vzdump de chaque hôte déclaré.
func (db *DB) ProxmoxBackupJobsByHost(ctx context.Context) (map[string][]proxmox.BackupJob, error) {
	rows, err := db.sql.QueryContext(ctx,
		`SELECT machine_id, `+proxmoxJobColumns+` FROM proxmox_backup_jobs ORDER BY machine_id, job_id`)
	if err != nil {
		return nil, fmt.Errorf("list proxmox backup jobs: %w", err)
	}
	type hosted struct {
		hostID string
		job    proxmox.BackupJob
	}
	jobs, err := collect(rows, func(row scanner) (hosted, error) {
		var entry hosted
		var rest jobScan
		if err := row.Scan(append([]any{&entry.hostID}, rest.destinations()...)...); err != nil {
			return hosted{}, err
		}
		entry.job = rest.result()
		return entry, nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan proxmox backup jobs: %w", err)
	}
	byHost := map[string][]proxmox.BackupJob{}
	for _, entry := range jobs {
		byHost[entry.hostID] = append(byHost[entry.hostID], entry.job)
	}
	return byHost, nil
}

// ProxmoxJobsUnreadable dit, pour chaque hôte déclaré, si son dernier relevé
// n'a pas pu lire les tâches de sauvegarde.
func (db *DB) ProxmoxJobsUnreadable(ctx context.Context) (map[string]bool, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT machine_id, backup_jobs_unreadable FROM machine_proxmox`)
	if err != nil {
		return nil, fmt.Errorf("read proxmox job access: %w", err)
	}
	type access struct {
		hostID     string
		unreadable bool
	}
	hosts, err := collect(rows, func(row scanner) (access, error) {
		var entry access
		err := row.Scan(&entry.hostID, &entry.unreadable)
		return entry, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan proxmox job access: %w", err)
	}
	byHost := map[string]bool{}
	for _, entry := range hosts {
		byHost[entry.hostID] = entry.unreadable
	}
	return byHost, nil
}
