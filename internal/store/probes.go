package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/probe"
)

const probeColumns = `p.id, p.name, p.kind, p.target, p.machine_id, COALESCE(m.name, ''),
	COALESCE(p.service_id, ''), COALESCE(s.name, ''), p.status,
	p.interval_seconds, p.timeout_seconds, p.failure_threshold, p.recovery_threshold,
	p.method, p.expected_status, p.expected_body, p.follow_redirects, p.tls,
	p.consecutive_failures, p.consecutive_successes,
	p.last_checked_at, p.last_duration_ms, p.last_code, p.last_reason,
	p.cert_subject, p.cert_issuer, p.cert_not_before, p.cert_not_after, p.cert_fingerprint,
	p.cert_chain_valid, p.cert_hostname_match, p.cert_ocsp,
	p.created_at`

const probeFrom = ` FROM probes p
	JOIN machines m ON m.id = p.machine_id
	LEFT JOIN services s ON s.id = p.service_id`

func (db *DB) InsertProbe(ctx context.Context, item probe.Probe) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO probes (id, name, kind, target, machine_id, service_id, status,
			interval_seconds, timeout_seconds, failure_threshold, recovery_threshold,
			method, expected_status, expected_body, follow_redirects, tls, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, item.Name, string(item.Kind), item.Target, item.MachineID, nullableString(item.ServiceID),
		string(item.Status), int64(item.Interval.Seconds()), int64(item.Timeout.Seconds()),
		item.FailureThreshold, item.RecoveryThreshold,
		item.Method, item.ExpectedStatus, item.ExpectedBody, boolToInt(item.FollowRedirects),
		boolToInt(item.TLS), item.CreatedAt.Unix())
	if err != nil {
		return fmt.Errorf("insert probe: %w", err)
	}
	return nil
}

// ListProbes rend les sondes d'une machine, ou de toutes si machineID est
// vide, par nom.
func (db *DB) ListProbes(ctx context.Context, machineID string) ([]probe.Probe, error) {
	query := `SELECT ` + probeColumns + probeFrom
	var args []any
	if machineID != "" {
		query += ` WHERE p.machine_id = ?`
		args = append(args, machineID)
	}
	rows, err := db.sql.QueryContext(ctx, query+` ORDER BY p.name, p.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("list probes: %w", err)
	}
	defer rows.Close()
	probes := []probe.Probe{}
	for rows.Next() {
		item, err := scanProbe(rows)
		if err != nil {
			return nil, err
		}
		probes = append(probes, item)
	}
	return probes, rows.Err()
}

func (db *DB) GetProbe(ctx context.Context, id string) (probe.Probe, error) {
	return scanProbe(db.sql.QueryRowContext(ctx, `SELECT `+probeColumns+probeFrom+` WHERE p.id = ?`, id))
}

// CountProbes rend le nombre de sondes et celles à traiter, hors ligne,
// sans charger les lignes : la barre latérale le demande à chaque page.
func (db *DB) CountProbes(ctx context.Context) (total, attention int, err error) {
	err = db.sql.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(status = ?), 0) FROM probes`, string(probe.StatusDown),
	).Scan(&total, &attention)
	if err != nil {
		return 0, 0, fmt.Errorf("count probes: %w", err)
	}
	return total, attention, nil
}

// CountProbeCertificates compte les certificats vus et ceux dont
// l'échéance tombe avant l'instant donné — les déjà expirés compris, que
// la plus proche échéance distingue. Une sonde en pause est écartée : ce
// qu'elle a vu ne dit plus rien de la cible.
func (db *DB) CountProbeCertificates(ctx context.Context, before time.Time) (probe.Certificates, error) {
	var counted probe.Certificates
	var soonest sql.NullInt64
	err := db.sql.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(cert_not_after < ?), 0), MIN(cert_not_after)
		FROM probes WHERE cert_fingerprint != '' AND status != ?`,
		before.Unix(), string(probe.StatusPaused),
	).Scan(&counted.Total, &counted.Expiring, &soonest)
	if err != nil {
		return probe.Certificates{}, fmt.Errorf("count probe certificates: %w", err)
	}
	counted.Soonest = timeOf(soonest)
	return counted, nil
}

func (db *DB) CountProbesOnMachine(ctx context.Context, machineID string) (int, error) {
	var carried int
	if err := db.sql.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM probes WHERE machine_id = ?`, machineID,
	).Scan(&carried); err != nil {
		return 0, fmt.Errorf("count probes on machine: %w", err)
	}
	return carried, nil
}

func (db *DB) DeleteProbe(ctx context.Context, id string) error {
	result, err := db.sql.ExecContext(ctx, `DELETE FROM probes WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete probe: %w", err)
	}
	return probeNotFoundIfNoRow(result)
}

// SetProbeStatus met l'état et remet les compteurs à zéro : après une
// pause, ce que la sonde valait ne dit plus rien de la cible.
func (db *DB) SetProbeStatus(ctx context.Context, id string, status probe.Status) error {
	result, err := db.sql.ExecContext(ctx,
		`UPDATE probes SET status = ?, consecutive_failures = 0, consecutive_successes = 0 WHERE id = ?`,
		string(status), id)
	if err != nil {
		return fmt.Errorf("set probe status: %w", err)
	}
	return probeNotFoundIfNoRow(result)
}

// ApplyChanges écrit les essais et l'état des sondes touchées en une
// transaction : ce qu'un signal rapporte est tout ou rien.
func (db *DB) ApplyProbeResults(ctx context.Context, changes probe.Changes) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin apply probes: %w", err)
	}
	defer tx.Rollback()
	for _, result := range changes.Results {
		if err := insertProbeResult(ctx, tx, result); err != nil {
			return err
		}
	}
	for _, item := range changes.Probes {
		if err := updateProbeState(ctx, tx, item); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit apply probes: %w", err)
	}
	return nil
}

// Un essai déjà en base, rejoué après une coupure, tombe sur la même clé
// et ne compte pas deux fois dans l'uptime.
func insertProbeResult(ctx context.Context, tx *sql.Tx, result probe.Result) error {
	_, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO probe_results (probe_id, checked_at, outcome, duration_ms, code, reason)
		VALUES (?, ?, ?, ?, ?, ?)`,
		result.ProbeID, result.CheckedAt.Unix(), string(result.Outcome), result.DurationMs,
		nullableInt(result.Code), string(result.Reason))
	if err != nil {
		return fmt.Errorf("insert probe result: %w", err)
	}
	return nil
}

func updateProbeState(ctx context.Context, tx *sql.Tx, item probe.Probe) error {
	certificate := item.Certificate
	if certificate == nil {
		certificate = &probe.Certificate{}
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE probes SET status = ?, consecutive_failures = ?, consecutive_successes = ?,
			last_checked_at = ?, last_duration_ms = ?, last_code = ?, last_reason = ?,
			cert_subject = ?, cert_issuer = ?, cert_not_before = ?, cert_not_after = ?, cert_fingerprint = ?,
			cert_chain_valid = ?, cert_hostname_match = ?, cert_ocsp = ?
		WHERE id = ?`,
		string(item.Status), item.ConsecutiveFailures, item.ConsecutiveSuccesses,
		nullableTime(item.LastCheckedAt), item.LastDurationMs, nullableInt(item.LastCode), string(item.LastReason),
		certificate.Subject, certificate.Issuer, nullableTime(certificate.NotBefore), nullableTime(certificate.NotAfter),
		certificate.Fingerprint, boolToInt(certificate.ChainValid), boolToInt(certificate.HostnameMatch),
		string(certificate.OCSP), item.ID)
	if err != nil {
		return fmt.Errorf("update probe state: %w", err)
	}
	return nil
}

// ListResults rend les derniers essais d'une sonde, le plus récent d'abord.
func (db *DB) ListProbeResults(ctx context.Context, probeID string, limit int) ([]probe.Result, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT checked_at, outcome, duration_ms, code, reason FROM probe_results
		WHERE probe_id = ? ORDER BY checked_at DESC LIMIT ?`, probeID, limit)
	if err != nil {
		return nil, fmt.Errorf("list probe results: %w", err)
	}
	defer rows.Close()
	results := []probe.Result{}
	for rows.Next() {
		result := probe.Result{ProbeID: probeID}
		var checkedAt int64
		var code sql.NullInt64
		var outcome, reason string
		if err := rows.Scan(&checkedAt, &outcome, &result.DurationMs, &code, &reason); err != nil {
			return nil, fmt.Errorf("scan probe result: %w", err)
		}
		result.CheckedAt = time.Unix(checkedAt, 0).UTC()
		result.Outcome = probe.Outcome(outcome)
		result.Reason = probe.Reason(reason)
		result.Code = intPointer(code)
		results = append(results, result)
	}
	return results, rows.Err()
}

// ListDays rend les jours agrégés d'une sonde, ou de toutes si probeID est
// vide, les plus anciens d'abord.
func (db *DB) ListProbeDays(ctx context.Context, probeID string, from time.Time) ([]probe.Day, error) {
	query := `SELECT probe_id, day, total, success, degraded, duration_ms FROM probe_days WHERE day >= ?`
	args := []any{from.Unix()}
	if probeID != "" {
		query += ` AND probe_id = ?`
		args = append(args, probeID)
	}
	rows, err := db.sql.QueryContext(ctx, query+` ORDER BY probe_id, day`, args...)
	if err != nil {
		return nil, fmt.Errorf("list probe days: %w", err)
	}
	defer rows.Close()
	days := []probe.Day{}
	for rows.Next() {
		var day probe.Day
		var at int64
		if err := rows.Scan(&day.ProbeID, &at, &day.Total, &day.Success, &day.Degraded, &day.DurationMs); err != nil {
			return nil, fmt.Errorf("scan probe day: %w", err)
		}
		day.Day = time.Unix(at, 0).UTC()
		days = append(days, day)
	}
	return days, rows.Err()
}

// CountResults compte les essais et les succès d'une fenêtre dans le brut ;
// un essai dégradé est un succès, la cible répond.
func (db *DB) CountProbeResults(ctx context.Context, probeID string, from, to time.Time) (total, success int, err error) {
	err = db.sql.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(outcome <> ?), 0) FROM probe_results
		WHERE probe_id = ? AND checked_at >= ? AND checked_at <= ?`,
		string(probe.OutcomeDown), probeID, from.Unix(), to.Unix()).Scan(&total, &success)
	if err != nil {
		return 0, 0, fmt.Errorf("count probe results: %w", err)
	}
	return total, success, nil
}

// CountDays additionne les jours d'une fenêtre : c'est pour cela que
// l'agrégat garde des comptes et non un pourcentage.
func (db *DB) CountProbeDays(ctx context.Context, probeID string, from time.Time) (total, success int, err error) {
	err = db.sql.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(total), 0), COALESCE(SUM(success), 0) FROM probe_days
		WHERE probe_id = ? AND day >= ?`, probeID, from.Unix()).Scan(&total, &success)
	if err != nil {
		return 0, 0, fmt.Errorf("count probe days: %w", err)
	}
	return total, success, nil
}

// RollupDay réécrit un jour entier depuis le brut, en une instruction :
// repasser dessus donne le même résultat. Un jour dont le brut a été
// purgé ne rend aucune ligne, donc ne réécrit rien.
func (db *DB) RollupProbeDay(ctx context.Context, dayStart, dayEnd time.Time) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO probe_days (probe_id, day, total, success, degraded, duration_ms)
		SELECT probe_id, ?, COUNT(*), SUM(outcome <> ?), SUM(outcome = ?), CAST(AVG(duration_ms) AS INTEGER)
		FROM probe_results WHERE checked_at >= ? AND checked_at < ?
		GROUP BY probe_id
		ON CONFLICT (probe_id, day) DO UPDATE SET
			total = excluded.total, success = excluded.success,
			degraded = excluded.degraded, duration_ms = excluded.duration_ms`,
		dayStart.Unix(), string(probe.OutcomeDown), string(probe.OutcomeDegraded),
		dayStart.Unix(), dayEnd.Unix())
	if err != nil {
		return fmt.Errorf("rollup probe day: %w", err)
	}
	return nil
}

func (db *DB) PurgeProbeHistory(ctx context.Context, resultsBefore, daysBefore time.Time) error {
	if _, err := db.sql.ExecContext(ctx, `DELETE FROM probe_results WHERE checked_at < ?`, resultsBefore.Unix()); err != nil {
		return fmt.Errorf("purge probe results: %w", err)
	}
	if _, err := db.sql.ExecContext(ctx, `DELETE FROM probe_days WHERE day < ?`, daysBefore.Unix()); err != nil {
		return fmt.Errorf("purge probe days: %w", err)
	}
	return nil
}

func scanProbe(row scanner) (probe.Probe, error) {
	var item probe.Probe
	var kind, status, reason string
	var intervalSeconds, timeoutSeconds int64
	var followRedirects, useTLS, chainValid, hostnameMatch int
	var lastCheckedAt, lastCode, notBefore, notAfter sql.NullInt64
	var certificate probe.Certificate
	var ocsp string
	var createdAt int64
	err := row.Scan(
		&item.ID, &item.Name, &kind, &item.Target, &item.MachineID, &item.MachineName,
		&item.ServiceID, &item.ServiceName, &status,
		&intervalSeconds, &timeoutSeconds, &item.FailureThreshold, &item.RecoveryThreshold,
		&item.Method, &item.ExpectedStatus, &item.ExpectedBody, &followRedirects, &useTLS,
		&item.ConsecutiveFailures, &item.ConsecutiveSuccesses,
		&lastCheckedAt, &item.LastDurationMs, &lastCode, &reason,
		&certificate.Subject, &certificate.Issuer, &notBefore, &notAfter, &certificate.Fingerprint,
		&chainValid, &hostnameMatch, &ocsp,
		&createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return probe.Probe{}, probe.ErrNotFound
	}
	if err != nil {
		return probe.Probe{}, fmt.Errorf("scan probe: %w", err)
	}
	item.Kind = probe.Kind(kind)
	item.Status = probe.Status(status)
	item.Interval = time.Duration(intervalSeconds) * time.Second
	item.Timeout = time.Duration(timeoutSeconds) * time.Second
	item.FollowRedirects = followRedirects != 0
	item.TLS = useTLS != 0
	item.LastCheckedAt = timeOf(lastCheckedAt)
	item.LastCode = intPointer(lastCode)
	item.LastReason = probe.Reason(reason)
	item.CreatedAt = time.Unix(createdAt, 0).UTC()
	if certificate.Fingerprint != "" {
		certificate.NotBefore = timeOf(notBefore)
		certificate.NotAfter = timeOf(notAfter)
		certificate.ChainValid = chainValid != 0
		certificate.HostnameMatch = hostnameMatch != 0
		certificate.OCSP = probe.OCSP(ocsp)
		item.Certificate = &certificate
	}
	return item, nil
}

func probeNotFoundIfNoRow(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return probe.ErrNotFound
	}
	return nil
}
