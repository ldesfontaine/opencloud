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
	COALESCE(p.service_id, ''), COALESCE(s.name, ''), COALESCE(sm.name, ''), p.status,
	p.interval_seconds, p.timeout_seconds, p.failure_threshold, p.recovery_threshold,
	p.method, p.expected_status, p.expected_body, p.follow_redirects, p.tls,
	p.consecutive_failures, p.consecutive_successes,
	p.last_checked_at, p.last_duration_ms, p.last_code, p.last_reason,
	c.subject, c.issuer, c.not_before, c.not_after, c.fingerprint,
	c.chain_valid, c.hostname_match, c.ocsp,
	p.created_at, p.revised_at`

const probeFrom = ` FROM probes p
	JOIN machines m ON m.id = p.machine_id
	LEFT JOIN declared_services s ON s.id = p.service_id
	LEFT JOIN machines sm ON sm.id = s.machine_id
	LEFT JOIN probe_certificates c ON c.probe_id = p.id`

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

// UpdateProbe remplace la définition en une seule instruction, sans relire
// ni réécrire l'état, qu'un essai peut changer au même moment. L'état,
// les compteurs et la date de révision ne s'écrivent que si l'édition remet
// à zéro, et une sonde en pause le reste. Le certificat part dans la même
// transaction, si la cible change.
func (db *DB) UpdateProbe(ctx context.Context, revision probe.Revision) error {
	item := revision.Probe
	resets := boolToInt(revision.ResetsState)
	return db.inTx(ctx, "update probe", func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
		UPDATE probes SET name = ?, kind = ?, target = ?, machine_id = ?, service_id = ?,
			interval_seconds = ?, timeout_seconds = ?, failure_threshold = ?, recovery_threshold = ?,
			method = ?, expected_status = ?, expected_body = ?, follow_redirects = ?, tls = ?,
			status = CASE WHEN ? AND status <> ? THEN ? ELSE status END,
			consecutive_failures = CASE WHEN ? THEN 0 ELSE consecutive_failures END,
			consecutive_successes = CASE WHEN ? THEN 0 ELSE consecutive_successes END,
			revised_at = CASE WHEN ? THEN ? ELSE revised_at END
		WHERE id = ?`,
			item.Name, string(item.Kind), item.Target, item.MachineID, nullableString(item.ServiceID),
			int64(item.Interval.Seconds()), int64(item.Timeout.Seconds()), item.FailureThreshold, item.RecoveryThreshold,
			item.Method, item.ExpectedStatus, item.ExpectedBody, boolToInt(item.FollowRedirects), boolToInt(item.TLS),
			resets, string(probe.StatusPaused), string(probe.StatusNew),
			resets,
			resets,
			resets, revisedAtMillis(item.RevisedAt),
			item.ID)
		if err != nil {
			return fmt.Errorf("update probe: %w", err)
		}
		if err := notFoundIfNoRow(result, probe.ErrNotFound); err != nil {
			return err
		}
		if !revision.ClearsCertificate {
			return nil
		}
		return deleteProbeCertificate(ctx, tx, item.ID)
	})
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
	return collect(rows, scanProbe)
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

// CountProbeCertificates compte les certificats vus, ceux dont l'échéance
// tombe entre maintenant et l'instant donné, et ceux qui l'ont déjà
// passée. La plus proche échéance rendue est à venir : celle d'un
// certificat expiré ne dirait pas dans combien de jours il expire. Une
// sonde en pause est écartée : ce qu'elle a vu ne dit plus rien.
func (db *DB) CountProbeCertificates(ctx context.Context, now, before time.Time) (probe.Certificates, error) {
	var counted probe.Certificates
	var soonest sql.NullInt64
	err := db.sql.QueryRowContext(ctx, `
		SELECT COUNT(*),
			COALESCE(SUM(c.not_after >= ? AND c.not_after < ?), 0),
			COALESCE(SUM(c.not_after < ?), 0),
			MIN(CASE WHEN c.not_after >= ? THEN c.not_after END)
		FROM probe_certificates c JOIN probes p ON p.id = c.probe_id WHERE p.status != ?`,
		now.Unix(), before.Unix(), now.Unix(), now.Unix(), string(probe.StatusPaused),
	).Scan(&counted.Total, &counted.Expiring, &counted.Expired, &soonest)
	if err != nil {
		return probe.Certificates{}, fmt.Errorf("count probe certificates: %w", err)
	}
	counted.Soonest = timeOf(soonest)
	return counted, nil
}

// DeclaredServiceExists dit si le service de l'annuaire existe : la sonde
// qui le vise le dit sur n'importe quelle machine.
func (db *DB) DeclaredServiceExists(ctx context.Context, id string) (bool, error) {
	var exists bool
	err := db.sql.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM declared_services WHERE id = ?)`, id).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check declared service: %w", err)
	}
	return exists, nil
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

// DeleteProbe relit le plan de la sonde et la supprime dans la même
// transaction : un composant de statut qui l'affiche la retient. Son
// historique part avec elle, ses alertes restent, détachées.
func (db *DB) DeleteProbe(ctx context.Context, id string) error {
	return db.inTx(ctx, "delete probe", func(tx *sql.Tx) error {
		plan, err := probeDeletionPlan(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := probe.RefuseDeletion(plan); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM probes WHERE id = ?`, id)
		if err != nil {
			return fmt.Errorf("delete probe: %w", err)
		}
		return notFoundIfNoRow(result, probe.ErrNotFound)
	})
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
	return notFoundIfNoRow(result, probe.ErrNotFound)
}

// ApplyProbeResults écrit les essais et l'état des sondes touchées en une
// transaction : ce qu'un signal rapporte est tout ou rien. Il rend les sondes
// dont l'état a réellement été écrit ; une sonde révisée depuis sa lecture
// n'y est pas, et personne ne doit la croire dans l'état qu'on lui avait
// calculé.
func (db *DB) ApplyProbeResults(ctx context.Context, changes probe.Changes) ([]probe.Probe, error) {
	written := make([]probe.Probe, 0, len(changes.Probes))
	err := db.inTx(ctx, "apply probes", func(tx *sql.Tx) error {
		for _, result := range changes.Results {
			if err := insertProbeResult(ctx, tx, result); err != nil {
				return err
			}
		}
		for _, item := range changes.Probes {
			isWritten, err := updateProbeState(ctx, tx, item)
			if err != nil {
				return err
			}
			if isWritten {
				written = append(written, item)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return written, nil
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

// L'état se calcule sur la sonde telle qu'elle était à la lecture : si une
// édition l'a remise à zéro depuis (revised_at a changé), il ne l'écrase
// pas, ni le certificat. Une révision qui laisse l'état en place ne change
// pas revised_at.
func updateProbeState(ctx context.Context, tx *sql.Tx, item probe.Probe) (bool, error) {
	result, err := tx.ExecContext(ctx, `
		UPDATE probes SET status = ?, consecutive_failures = ?, consecutive_successes = ?,
			last_checked_at = ?, last_duration_ms = ?, last_code = ?, last_reason = ?
		WHERE id = ? AND revised_at IS ?`,
		string(item.Status), item.ConsecutiveFailures, item.ConsecutiveSuccesses,
		nullableTime(item.LastCheckedAt), item.LastDurationMs, nullableInt(item.LastCode), string(item.LastReason),
		item.ID, revisedAtMillis(item.RevisedAt))
	if err != nil {
		return false, fmt.Errorf("update probe state: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count updated probe state: %w", err)
	}
	if affected == 0 {
		return false, nil
	}
	return true, saveProbeCertificate(ctx, tx, item.ID, item.Certificate)
}

// saveProbeCertificate garde le dernier certificat vu ; un essai qui n'en a
// pas vu efface le précédent. Un certificat sans empreinte n'en est pas un.
func saveProbeCertificate(ctx context.Context, tx *sql.Tx, probeID string, certificate *probe.Certificate) error {
	if certificate == nil || certificate.Fingerprint == "" {
		return deleteProbeCertificate(ctx, tx, probeID)
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO probe_certificates (probe_id, subject, issuer, not_before, not_after, fingerprint,
			chain_valid, hostname_match, ocsp)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (probe_id) DO UPDATE SET
			subject = excluded.subject, issuer = excluded.issuer,
			not_before = excluded.not_before, not_after = excluded.not_after, fingerprint = excluded.fingerprint,
			chain_valid = excluded.chain_valid, hostname_match = excluded.hostname_match, ocsp = excluded.ocsp`,
		probeID, certificate.Subject, certificate.Issuer, nullableTime(certificate.NotBefore), nullableTime(certificate.NotAfter),
		certificate.Fingerprint, boolToInt(certificate.ChainValid), boolToInt(certificate.HostnameMatch),
		string(certificate.OCSP))
	if err != nil {
		return fmt.Errorf("save probe certificate: %w", err)
	}
	return nil
}

func deleteProbeCertificate(ctx context.Context, tx *sql.Tx, probeID string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM probe_certificates WHERE probe_id = ?`, probeID); err != nil {
		return fmt.Errorf("delete probe certificate: %w", err)
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
	return collect(rows, func(row scanner) (probe.Result, error) { return scanProbeResult(row, probeID) })
}

func scanProbeResult(row scanner, probeID string) (probe.Result, error) {
	result := probe.Result{ProbeID: probeID}
	var checkedAt int64
	var code sql.NullInt64
	var outcome, reason string
	if err := row.Scan(&checkedAt, &outcome, &result.DurationMs, &code, &reason); err != nil {
		return probe.Result{}, fmt.Errorf("scan probe result: %w", err)
	}
	result.CheckedAt = time.Unix(checkedAt, 0).UTC()
	result.Outcome = probe.Outcome(outcome)
	result.Reason = probe.Reason(reason)
	result.Code = intPointer(code)
	return result, nil
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
	return collect(rows, scanProbeDay)
}

func scanProbeDay(row scanner) (probe.Day, error) {
	var day probe.Day
	var at int64
	if err := row.Scan(&day.ProbeID, &at, &day.Total, &day.Success, &day.Degraded, &day.DurationMs); err != nil {
		return probe.Day{}, fmt.Errorf("scan probe day: %w", err)
	}
	day.Day = time.Unix(at, 0).UTC()
	return day, nil
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

// CountProbeWindow compte une fenêtre de plusieurs jours : les jours agrégés
// complets, puis le brut depuis la fin du dernier d'entre eux. Le jour courant
// n'est jamais lu dans l'agrégat (il est réécrit toutes les 5 min, donc en
// retard) ; un jour agrégé n'est jamais relu dans le brut, qui le garde
// pourtant 7 jours : pas de double compte. Sans jour agrégé dans la
// fenêtre, tout vient du brut. L'agrégat garde des comptes et non un
// pourcentage pour que cette addition soit juste.
func (db *DB) CountProbeWindow(ctx context.Context, probeID string, from, now time.Time) (total, success int, err error) {
	now = now.UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	var lastDay sql.NullInt64
	err = db.sql.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(total), 0), COALESCE(SUM(success), 0), MAX(day) FROM probe_days
		WHERE probe_id = ? AND day >= ? AND day < ?`,
		probeID, from.Unix(), today.Unix()).Scan(&total, &success, &lastDay)
	if err != nil {
		return 0, 0, fmt.Errorf("count probe days: %w", err)
	}
	rawFrom := from
	if lastDay.Valid {
		rawFrom = time.Unix(lastDay.Int64, 0).UTC().AddDate(0, 0, 1)
	}
	rawTotal, rawSuccess, err := db.CountProbeResults(ctx, probeID, rawFrom, now)
	if err != nil {
		return 0, 0, err
	}
	return total + rawTotal, success + rawSuccess, nil
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
	var followRedirects, useTLS int
	var lastCheckedAt, lastCode, notBefore, notAfter, chainValid, hostnameMatch sql.NullInt64
	var subject, issuer, fingerprint, ocsp sql.NullString
	var createdAt int64
	var revisedAt sql.NullInt64
	err := row.Scan(
		&item.ID, &item.Name, &kind, &item.Target, &item.MachineID, &item.MachineName,
		&item.ServiceID, &item.ServiceName, &item.ServiceMachineName, &status,
		&intervalSeconds, &timeoutSeconds, &item.FailureThreshold, &item.RecoveryThreshold,
		&item.Method, &item.ExpectedStatus, &item.ExpectedBody, &followRedirects, &useTLS,
		&item.ConsecutiveFailures, &item.ConsecutiveSuccesses,
		&lastCheckedAt, &item.LastDurationMs, &lastCode, &reason,
		&subject, &issuer, &notBefore, &notAfter, &fingerprint,
		&chainValid, &hostnameMatch, &ocsp,
		&createdAt, &revisedAt,
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
	item.RevisedAt = timeOfMillis(revisedAt)
	// Sans ligne dans probe_certificates, la jointure rend des NULL : pas
	// de certificat.
	if fingerprint.Valid {
		item.Certificate = &probe.Certificate{
			Subject:       subject.String,
			Issuer:        issuer.String,
			NotBefore:     timeOf(notBefore),
			NotAfter:      timeOf(notAfter),
			Fingerprint:   fingerprint.String,
			ChainValid:    chainValid.Int64 != 0,
			HostnameMatch: hostnameMatch.Int64 != 0,
			OCSP:          probe.OCSP(ocsp.String),
		}
	}
	return item, nil
}

// revised_at est en millisecondes, contrairement aux autres dates : la garde
// qui écarte un essai de l'ancien réglage ne doit pas se tromper dans la
// seconde de l'édition.
func revisedAtMillis(at time.Time) any {
	if at.IsZero() {
		return nil
	}
	return at.UnixMilli()
}

func timeOfMillis(value sql.NullInt64) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return time.UnixMilli(value.Int64)
}
