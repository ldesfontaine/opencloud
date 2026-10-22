package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/deletion"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/status"
)

// --- Pages ---

const pageColumns = `id, project_id, slug, title, announcement, language, created_at, updated_at`

// InsertPage écrit la page après avoir vérifié, dans sa transaction, que le
// slug est libre et que le projet existe et n'a pas déjà sa page : chaque
// refus garde sa clé.
func (db *DB) InsertPage(ctx context.Context, page status.Page) error {
	return db.inTx(ctx, "insert status page", func(tx *sql.Tx) error {
		if err := checkPageKeysFree(ctx, tx, page); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO status_pages (`+pageColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			page.ID, nullableString(page.ProjectID), page.Slug, page.Title, page.Announcement, string(page.Language),
			page.CreatedAt.Unix(), page.UpdatedAt.Unix())
		if err != nil {
			return fmt.Errorf("insert status page: %w", err)
		}
		return nil
	})
}

// UpdatePage réécrit tout ce que l'opérateur règle ; les composants restent.
func (db *DB) UpdatePage(ctx context.Context, page status.Page) error {
	return db.inTx(ctx, "update status page", func(tx *sql.Tx) error {
		var found int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM status_pages WHERE id = ?`, page.ID).Scan(&found); err != nil {
			return fmt.Errorf("find status page: %w", err)
		}
		if found == 0 {
			return status.ErrNotFound
		}
		if err := checkPageKeysFree(ctx, tx, page); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
			UPDATE status_pages SET project_id = ?, slug = ?, title = ?, announcement = ?, language = ?, updated_at = ?
			WHERE id = ?`,
			nullableString(page.ProjectID), page.Slug, page.Title, page.Announcement, string(page.Language), page.UpdatedAt.Unix(), page.ID)
		if err != nil {
			return fmt.Errorf("update status page: %w", err)
		}
		return nil
	})
}

// checkPageKeysFree refuse un slug porté par une autre page, un projet
// inconnu, ou un projet qui a déjà une autre page. Les index uniques et la
// clé étrangère restent le filet.
func checkPageKeysFree(ctx context.Context, tx *sql.Tx, page status.Page) error {
	var taken int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM status_pages WHERE slug = ? AND id != ?`, page.Slug, page.ID).Scan(&taken); err != nil {
		return fmt.Errorf("check status page slug: %w", err)
	}
	if taken > 0 {
		return status.ErrSlugTaken
	}
	if page.ProjectID == "" {
		return nil
	}
	var projects int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id = ?`, page.ProjectID).Scan(&projects); err != nil {
		return fmt.Errorf("find project: %w", err)
	}
	if projects == 0 {
		return status.ErrProjectInvalid
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM status_pages WHERE project_id = ? AND id != ?`, page.ProjectID, page.ID).Scan(&taken); err != nil {
		return fmt.Errorf("check status page project: %w", err)
	}
	if taken > 0 {
		return status.ErrProjectTaken
	}
	return nil
}

func (db *DB) ListPages(ctx context.Context) ([]status.Page, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+pageColumns+` FROM status_pages ORDER BY slug`)
	if err != nil {
		return nil, fmt.Errorf("list status pages: %w", err)
	}
	return collect(rows, scanPage)
}

func (db *DB) GetPage(ctx context.Context, id string) (status.Page, error) {
	return scanPage(db.sql.QueryRowContext(ctx, `SELECT `+pageColumns+` FROM status_pages WHERE id = ?`, id))
}

func (db *DB) GetPageBySlug(ctx context.Context, slug string) (status.Page, error) {
	return scanPage(db.sql.QueryRowContext(ctx, `SELECT `+pageColumns+` FROM status_pages WHERE slug = ?`, slug))
}

func scanPage(row scanner) (status.Page, error) {
	var page status.Page
	var projectID sql.NullString
	var language string
	var createdAt, updatedAt int64
	err := row.Scan(&page.ID, &projectID, &page.Slug, &page.Title, &page.Announcement, &language, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return status.Page{}, status.ErrNotFound
	}
	if err != nil {
		return status.Page{}, fmt.Errorf("scan status page: %w", err)
	}
	page.ProjectID = projectID.String
	page.Language = lang.Code(language)
	page.CreatedAt = time.Unix(createdAt, 0)
	page.UpdatedAt = time.Unix(updatedAt, 0)
	return page, nil
}

// DeletePage efface la page ; ses composants et leurs membres partent en
// cascade, les incidents qui les citaient restent.
func (db *DB) DeletePage(ctx context.Context, id string) error {
	result, err := db.sql.ExecContext(ctx, `DELETE FROM status_pages WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete status page: %w", err)
	}
	return notFoundIfNoRow(result, status.ErrNotFound)
}

func (db *DB) CountPages(ctx context.Context) (int, error) {
	var count int
	if err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM status_pages`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count status pages: %w", err)
	}
	return count, nil
}

// PageDeletionPlan : une page ne retient rien ; elle emporte ses composants
// et leurs membres.
func (db *DB) PageDeletionPlan(ctx context.Context, id string) (deletion.Plan, error) {
	return readPlan(ctx, db.sql, id, nil,
		[]planQuery{
			{deletion.KindComponents, `SELECT COUNT(*) FROM status_components WHERE page_id = ?`},
			{deletion.KindMembers, `SELECT COUNT(*) FROM status_component_members
				WHERE component_id IN (SELECT id FROM status_components WHERE page_id = ?)`},
		},
		nil)
}
