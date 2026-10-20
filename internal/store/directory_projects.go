package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ldesfontaine/opencloud/internal/directory"
)

func (db *DB) InsertProject(ctx context.Context, project directory.Project) error {
	return db.inTx(ctx, "insert project", func(tx *sql.Tx) error {
		if err := checkProjectNameFree(ctx, tx, project); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO projects (id, name, title, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
			project.ID, project.Name, project.Title, project.CreatedAt.Unix(), project.UpdatedAt.Unix())
		if err != nil {
			return fmt.Errorf("insert project: %w", err)
		}
		return nil
	})
}

// UpdateProject réécrit le nom et le titre ; les liens de services ne se
// règlent que depuis le service.
func (db *DB) UpdateProject(ctx context.Context, project directory.Project) error {
	return db.inTx(ctx, "update project", func(tx *sql.Tx) error {
		var found int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id = ?`, project.ID).Scan(&found); err != nil {
			return fmt.Errorf("find project: %w", err)
		}
		if found == 0 {
			return directory.ErrProjectNotFound
		}
		if err := checkProjectNameFree(ctx, tx, project); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE projects SET name = ?, title = ?, updated_at = ? WHERE id = ?`,
			project.Name, project.Title, project.UpdatedAt.Unix(), project.ID)
		if err != nil {
			return fmt.Errorf("update project: %w", err)
		}
		return nil
	})
}

func checkProjectNameFree(ctx context.Context, tx *sql.Tx, project directory.Project) error {
	var others int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE name = ? AND id != ?`,
		project.Name, project.ID).Scan(&others)
	if err != nil {
		return fmt.Errorf("check project name: %w", err)
	}
	if others > 0 {
		return directory.ErrProjectNameTaken
	}
	return nil
}

func (db *DB) GetProject(ctx context.Context, id string) (directory.Project, error) {
	row := db.sql.QueryRowContext(ctx, `SELECT id, name, title, created_at, updated_at FROM projects WHERE id = ?`, id)
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
	rows, err := db.sql.QueryContext(ctx, `SELECT id, name, title, created_at, updated_at FROM projects ORDER BY name, id`)
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
	var createdAt, updatedAt int64
	if err := row.Scan(&project.ID, &project.Name, &project.Title, &createdAt, &updatedAt); err != nil {
		return directory.Project{}, err
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

// DeleteProject défait le groupe : ses liens partent, ses services restent.
func (db *DB) DeleteProject(ctx context.Context, id string) error {
	result, err := db.sql.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	return notFoundIfNoRow(result, directory.ErrProjectNotFound)
}
