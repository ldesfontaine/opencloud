package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/restic"
	"github.com/ldesfontaine/opencloud/internal/seal"
)

// Les mots de passe d'un lien restic, scellés chacun pour sa colonne et sa
// ligne : recopiés dans un autre lien, ils ne s'ouvrent plus.
func resticLinkScope(column, id string) seal.Scope {
	return seal.Scope{Table: "restic_links", Column: column, Row: id}
}

const resticLinkColumns = `id, source_machine_id, target_machine_id, created_at, retired_at, retain_at, retain_run_id, retain_state, retain_reason`

// scanResticLink lit un lien sans ses mots de passe.
func scanResticLink(row scanner) (restic.Link, error) {
	var link restic.Link
	var createdAt int64
	var retiredAt, retainAt sql.NullInt64
	err := row.Scan(&link.ID, &link.SourceMachineID, &link.TargetMachineID, &createdAt, &retiredAt, &retainAt,
		&link.Retain.RunID, &link.Retain.State, &link.Retain.Reason)
	link.CreatedAt = time.Unix(createdAt, 0)
	link.RetiredAt = timeOf(retiredAt)
	link.Retain.At = timeOf(retainAt)
	return link, err
}

// EnsureResticLink pose le lien s'il n'existe pas, ses deux mots de passe
// scellés, puis relit celui qui est en base : deux créations ensemble
// tombent sur le même.
func (db *DB) EnsureResticLink(ctx context.Context, link restic.Link) (restic.Link, bool, error) {
	restSealed, err := db.sealSecret(link.Secrets.REST, resticLinkScope("rest_password", link.ID))
	if err != nil {
		return restic.Link{}, false, fmt.Errorf("seal restic rest password: %w", err)
	}
	repoSealed, err := db.sealSecret(link.Secrets.Repo, resticLinkScope("repo_password", link.ID))
	if err != nil {
		return restic.Link{}, false, fmt.Errorf("seal restic repo password: %w", err)
	}
	result, err := db.sql.ExecContext(ctx, `
		INSERT INTO restic_links (id, source_machine_id, target_machine_id, rest_password, repo_password, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (source_machine_id, target_machine_id) DO NOTHING`,
		link.ID, link.SourceMachineID, link.TargetMachineID, restSealed, repoSealed, link.CreatedAt.Unix())
	if err != nil {
		return restic.Link{}, false, fmt.Errorf("insert restic link: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return restic.Link{}, false, err
	}
	stored, err := db.GetResticLink(ctx, link.SourceMachineID, link.TargetMachineID)
	return stored, inserted > 0, err
}

// GetResticLink rend le lien, retiré ou non, avec ses mots de passe ouverts :
// pour un ordre, jamais pour une lecture d'écran.
func (db *DB) GetResticLink(ctx context.Context, sourceID, targetID string) (restic.Link, error) {
	var link restic.Link
	var createdAt int64
	var retiredAt, retainAt sql.NullInt64
	var restSealed, repoSealed string
	err := db.sql.QueryRowContext(ctx, `SELECT `+resticLinkColumns+`, rest_password, repo_password
		FROM restic_links WHERE source_machine_id = ? AND target_machine_id = ?`, sourceID, targetID).Scan(
		&link.ID, &link.SourceMachineID, &link.TargetMachineID, &createdAt, &retiredAt, &retainAt,
		&link.Retain.RunID, &link.Retain.State, &link.Retain.Reason, &restSealed, &repoSealed)
	if errors.Is(err, sql.ErrNoRows) {
		return restic.Link{}, restic.ErrLinkNotFound
	}
	if err != nil {
		return restic.Link{}, fmt.Errorf("get restic link: %w", err)
	}
	link.CreatedAt = time.Unix(createdAt, 0)
	link.RetiredAt = timeOf(retiredAt)
	link.Retain.At = timeOf(retainAt)
	link.Secrets.REST, err = db.openSecret(restSealed, resticLinkScope("rest_password", link.ID))
	if err != nil {
		return restic.Link{}, err
	}
	link.Secrets.Repo, err = db.openSecret(repoSealed, resticLinkScope("repo_password", link.ID))
	if err != nil {
		return restic.Link{}, err
	}
	return link, nil
}

// ListResticLinks rend les liens en service : un lien retiré n'a ni
// sauvegarde, ni rétention, ni place à l'écran.
func (db *DB) ListResticLinks(ctx context.Context) ([]restic.Link, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+resticLinkColumns+` FROM restic_links
		WHERE retired_at IS NULL ORDER BY source_machine_id, target_machine_id`)
	if err != nil {
		return nil, fmt.Errorf("list restic links: %w", err)
	}
	return collect(rows, scanResticLink)
}

func (db *DB) DeleteResticLink(ctx context.Context, sourceID, targetID string) error {
	result, err := db.sql.ExecContext(ctx, `DELETE FROM restic_links WHERE source_machine_id = ? AND target_machine_id = ?`, sourceID, targetID)
	if err != nil {
		return fmt.Errorf("delete restic link: %w", err)
	}
	return notFoundIfNoRow(result, restic.ErrLinkNotFound)
}

// SetResticLinkRetired retire le lien (at), ou le remet en service (zéro).
// Ses mots de passe restent scellés dans les deux cas.
func (db *DB) SetResticLinkRetired(ctx context.Context, linkID string, at time.Time) error {
	result, err := db.sql.ExecContext(ctx, `UPDATE restic_links SET retired_at = ? WHERE id = ?`, nullableTime(at), linkID)
	if err != nil {
		return fmt.Errorf("set restic link retired: %w", err)
	}
	return notFoundIfNoRow(result, restic.ErrLinkNotFound)
}

func (db *DB) NoteResticRetain(ctx context.Context, linkID string, outcome restic.RetainOutcome) error {
	result, err := db.sql.ExecContext(ctx, `
		UPDATE restic_links SET retain_at = ?, retain_run_id = ?, retain_state = ?, retain_reason = ? WHERE id = ?`,
		nullableTime(outcome.At), outcome.RunID, outcome.State, outcome.Reason, linkID)
	if err != nil {
		return fmt.Errorf("note restic retain: %w", err)
	}
	return notFoundIfNoRow(result, restic.ErrLinkNotFound)
}
