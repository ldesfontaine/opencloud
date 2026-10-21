package directory

import (
	"context"
	"fmt"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/deletion"
)

func (d *Directory) CreateProject(ctx context.Context, definition ProjectDefinition) (Project, error) {
	definition = definition.Complete()
	if err := definition.Validate(); err != nil {
		return Project{}, err
	}
	id, err := NewID(ProjectPrefix)
	if err != nil {
		return Project{}, fmt.Errorf("generate project id: %w", err)
	}
	now := d.now()
	created := Project{ID: id, Name: definition.Name, Title: definition.Title, CreatedAt: now, UpdatedAt: now}
	if err := d.store.InsertProject(ctx, created); err != nil {
		return Project{}, err
	}
	d.logger.Info("project created", "project_id", id, "name", created.Name)
	d.auditor.Record(ctx, projectEvent(audit.ProjectCreate, created))
	d.changed()
	return d.store.GetProject(ctx, id)
}

func (d *Directory) UpdateProject(ctx context.Context, id string, definition ProjectDefinition) (Project, error) {
	before, err := d.store.GetProject(ctx, id)
	if err != nil {
		return Project{}, err
	}
	definition = definition.Complete()
	if err := definition.Validate(); err != nil {
		return Project{}, err
	}
	before.Name = definition.Name
	before.Title = definition.Title
	before.UpdatedAt = d.now()
	if err := d.store.UpdateProject(ctx, before); err != nil {
		return Project{}, err
	}
	d.logger.Info("project updated", "project_id", id, "name", before.Name)
	d.auditor.Record(ctx, projectEvent(audit.ProjectUpdate, before))
	d.changed()
	return d.store.GetProject(ctx, id)
}

// DeleteProjectPlan dit ce que DeleteProject ferait : rien ne le retient,
// seuls les liens vers ses services partent.
func (d *Directory) DeleteProjectPlan(ctx context.Context, id string) (deletion.Plan, error) {
	if _, err := d.store.GetProject(ctx, id); err != nil {
		return deletion.Plan{}, err
	}
	return d.store.ProjectDeletionPlan(ctx, id)
}

// DeleteProject défait le groupe et rien d'autre : les services qu'il
// regroupait restent dans l'annuaire.
func (d *Directory) DeleteProject(ctx context.Context, id string) error {
	found, err := d.store.GetProject(ctx, id)
	if err != nil {
		return err
	}
	if err := d.store.DeleteProject(ctx, id); err != nil {
		return err
	}
	d.logger.Info("project deleted", "project_id", id, "name", found.Name, "services", len(found.ServiceIDs))
	d.auditor.Record(ctx, projectEvent(audit.ProjectDelete, found))
	d.changed()
	return nil
}
