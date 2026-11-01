package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/lang"
)

const projectColumns = `id, name, title, slug, announcement, language, published_at, created_at, updated_at`

func (db *DB) InsertProject(ctx context.Context, project directory.Project) error {
	return db.inTx(ctx, "insert project", func(tx *sql.Tx) error {
		if err := checkProjectKeysFree(ctx, tx, project); err != nil {
			return err
		}
		publication := project.Publication
		_, err := tx.ExecContext(ctx,
			`INSERT INTO projects (`+projectColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			project.ID, project.Name, project.Title, nullableString(publication.Slug), publication.Announcement,
			string(publication.Language), nullableTime(publication.PublishedAt), project.CreatedAt.Unix(), project.UpdatedAt.Unix())
		if err != nil {
			return fmt.Errorf("insert project: %w", err)
		}
		return nil
	})
}

// UpdateProject réécrit le nom, le titre et la publication ; les liens de
// services ne se règlent que depuis le service.
func (db *DB) UpdateProject(ctx context.Context, project directory.Project) error {
	return db.inTx(ctx, "update project", func(tx *sql.Tx) error {
		var found int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id = ?`, project.ID).Scan(&found); err != nil {
			return fmt.Errorf("find project: %w", err)
		}
		if found == 0 {
			return directory.ErrProjectNotFound
		}
		if err := checkProjectKeysFree(ctx, tx, project); err != nil {
			return err
		}
		publication := project.Publication
		_, err := tx.ExecContext(ctx, `
			UPDATE projects SET name = ?, title = ?, slug = ?, announcement = ?, language = ?, published_at = ?, updated_at = ?
			WHERE id = ?`,
			project.Name, project.Title, nullableString(publication.Slug), publication.Announcement, string(publication.Language),
			nullableTime(publication.PublishedAt), project.UpdatedAt.Unix(), project.ID)
		if err != nil {
			return fmt.Errorf("update project: %w", err)
		}
		return nil
	})
}

// checkProjectKeysFree refuse un nom ou un slug porté par un autre projet, et
// une publication de plus quand le plafond est atteint. Les index uniques
// restent le filet.
func checkProjectKeysFree(ctx context.Context, tx *sql.Tx, project directory.Project) error {
	var others int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE name = ? AND id != ?`,
		project.Name, project.ID).Scan(&others)
	if err != nil {
		return fmt.Errorf("check project name: %w", err)
	}
	if others > 0 {
		return directory.ErrProjectNameTaken
	}
	if !project.Publication.IsPublished() {
		return nil
	}
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE slug = ? AND id != ?`,
		project.Publication.Slug, project.ID).Scan(&others)
	if err != nil {
		return fmt.Errorf("check project slug: %w", err)
	}
	if others > 0 {
		return directory.ErrSlugTaken
	}
	return checkPublicationCeiling(ctx, tx, project)
}

// checkPublicationCeiling ne compte que les autres projets publiés : un projet
// déjà publié garde sa place quand il se réécrit.
func checkPublicationCeiling(ctx context.Context, tx *sql.Tx, project directory.Project) error {
	var published int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE slug IS NOT NULL AND id != ?`, project.ID).Scan(&published)
	if err != nil {
		return fmt.Errorf("count publications: %w", err)
	}
	if published >= directory.MaxPublications {
		return directory.ErrTooManyPublications
	}
	return nil
}

func (db *DB) GetProject(ctx context.Context, id string) (directory.Project, error) {
	row := db.sql.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM projects WHERE id = ?`, id)
	project, err := scanProject(row)
	if errors.Is(err, sql.ErrNoRows) {
		return directory.Project{}, directory.ErrProjectNotFound
	}
	if err != nil {
		return directory.Project{}, err
	}
	projects := []directory.Project{project}
	if err := db.attachProjectServices(ctx, projects, id); err != nil {
		return directory.Project{}, err
	}
	return projects[0], nil
}

func (db *DB) ListProjects(ctx context.Context) ([]directory.Project, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+projectColumns+` FROM projects ORDER BY name, id`)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	projects, err := collect(rows, scanProject)
	if err != nil {
		return nil, err
	}
	if err := db.attachProjectServices(ctx, projects, ""); err != nil {
		return nil, err
	}
	return projects, nil
}

func scanProject(row scanner) (directory.Project, error) {
	var project directory.Project
	var slug sql.NullString
	var language string
	var publishedAt sql.NullInt64
	var createdAt, updatedAt int64
	err := row.Scan(&project.ID, &project.Name, &project.Title, &slug, &project.Publication.Announcement, &language, &publishedAt, &createdAt, &updatedAt)
	if err != nil {
		return directory.Project{}, err
	}
	project.Publication.Slug = slug.String
	project.Publication.Language = lang.Code(language)
	if publishedAt.Valid {
		project.Publication.PublishedAt = time.Unix(publishedAt.Int64, 0)
	}
	project.ServiceIDs = []string{}
	project.CreatedAt = time.Unix(createdAt, 0)
	project.UpdatedAt = time.Unix(updatedAt, 0)
	return project, nil
}

// attachProjectServices remplit les services de chaque projet lu, par nom
// de service ; projectID vide les lit pour tous.
func (db *DB) attachProjectServices(ctx context.Context, projects []directory.Project, projectID string) error {
	query := `SELECT ps.project_id, ps.service_id FROM project_services ps JOIN declared_services s ON s.id = ps.service_id`
	var args []any
	if projectID != "" {
		query += ` WHERE ps.project_id = ?`
		args = append(args, projectID)
	}
	links, err := db.listServiceLinks(ctx, query+` ORDER BY ps.project_id, s.name, s.machine_id, s.id`, args...)
	if err != nil {
		return err
	}
	position := make(map[string]int, len(projects))
	for index, project := range projects {
		position[project.ID] = index
	}
	for _, link := range links {
		if index, ok := position[link.owner]; ok {
			projects[index].ServiceIDs = append(projects[index].ServiceIDs, link.member)
		}
	}
	return nil
}

// DeleteProject défait le groupe : ses liens, sa page publique et ses
// composants partent, ses services restent.
func (db *DB) DeleteProject(ctx context.Context, id string) error {
	result, err := db.sql.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	return notFoundIfNoRow(result, directory.ErrProjectNotFound)
}
