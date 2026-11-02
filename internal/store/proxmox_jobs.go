package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/proxmox"
)

// guestIDSeparator sépare les numéros d'invités d'une tâche dans sa colonne.
const guestIDSeparator = ","

// replaceProxmoxBackupJobs remplace les tâches vzdump de l'hôte, dans la
// transaction du relevé.
func replaceProxmoxBackupJobs(ctx context.Context, tx *sql.Tx, machineID string, jobs []proxmox.BackupJob) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM proxmox_backup_jobs WHERE machine_id = ?`, machineID); err != nil {
		return fmt.Errorf("delete proxmox backup jobs: %w", err)
	}
	for _, job := range jobs {
		retention := job.Retention
		_, err := tx.ExecContext(ctx, `
			INSERT INTO proxmox_backup_jobs (machine_id, job_id, schedule, next_run_at, storage, enabled, node, all_guests,
				vmids, excluded, retention_known, keep_all, keep_last, keep_daily, keep_weekly, keep_monthly, keep_yearly, comment)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (machine_id, job_id) DO NOTHING`,
			machineID, job.ID, job.Schedule, nullableTime(job.NextRun), job.Storage, job.Enabled, job.Node, job.AllGuests,
			joinGuestIDs(job.VMIDs), joinGuestIDs(job.Excluded), retention.Known, retention.KeepAll, retention.Last,
			retention.Daily, retention.Weekly, retention.Monthly, retention.Yearly, job.Comment)
		if err != nil {
			return fmt.Errorf("insert proxmox backup job %s: %w", job.ID, err)
		}
	}
	return nil
}

const proxmoxJobColumns = `job_id, schedule, next_run_at, storage, enabled, node, all_guests, vmids, excluded,
	retention_known, keep_all, keep_last, keep_daily, keep_weekly, keep_monthly, keep_yearly, comment`

func (db *DB) listProxmoxBackupJobs(ctx context.Context, machineID string) ([]proxmox.BackupJob, error) {
	rows, err := db.sql.QueryContext(ctx,
		`SELECT `+proxmoxJobColumns+` FROM proxmox_backup_jobs WHERE machine_id = ? ORDER BY job_id`, machineID)
	if err != nil {
		return nil, fmt.Errorf("list proxmox backup jobs: %w", err)
	}
	jobs, err := collect(rows, scanProxmoxBackupJob)
	if err != nil {
		return nil, fmt.Errorf("scan proxmox backup jobs: %w", err)
	}
	return jobs, nil
}

func scanProxmoxBackupJob(row scanner) (proxmox.BackupJob, error) {
	var read jobScan
	if err := row.Scan(read.destinations()...); err != nil {
		return proxmox.BackupJob{}, err
	}
	return read.result(), nil
}

// jobScan lit les colonnes de proxmoxJobColumns ; une requête qui en lit
// d'autres les pose devant destinations().
type jobScan struct {
	job      proxmox.BackupJob
	nextRun  sql.NullInt64
	vmids    string
	excluded string
}

func (read *jobScan) destinations() []any {
	job, retention := &read.job, &read.job.Retention
	return []any{&job.ID, &job.Schedule, &read.nextRun, &job.Storage, &job.Enabled, &job.Node, &job.AllGuests, &read.vmids,
		&read.excluded, &retention.Known, &retention.KeepAll, &retention.Last, &retention.Daily, &retention.Weekly,
		&retention.Monthly, &retention.Yearly, &job.Comment}
}

func (read *jobScan) result() proxmox.BackupJob {
	job := read.job
	job.NextRun = timeOf(read.nextRun)
	job.VMIDs = splitGuestIDs(read.vmids)
	job.Excluded = splitGuestIDs(read.excluded)
	return job
}

func joinGuestIDs(ids []int) string {
	texts := make([]string, 0, len(ids))
	for _, id := range ids {
		texts = append(texts, strconv.Itoa(id))
	}
	return strings.Join(texts, guestIDSeparator)
}

func splitGuestIDs(text string) []int {
	ids := []int{}
	for item := range strings.SplitSeq(text, guestIDSeparator) {
		if id, err := strconv.Atoi(item); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}
