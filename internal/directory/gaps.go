package directory

import (
	"context"
	"errors"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/update"
)

// Gaps compare ce qui est déclaré à ce que les machines montrent. Avec un
// projet, les conteneurs non déclarés ne sont pas dits : ils n'ont pas de
// projet.
func (d *Directory) Gaps(ctx context.Context, filter Filter) (GapReport, error) {
	declared, err := d.store.ListDeclaredServices(ctx, filter)
	if err != nil {
		return GapReport{}, err
	}
	containers, checks, err := d.observed(ctx, filter.MachineID)
	if err != nil {
		return GapReport{}, err
	}
	ignored, err := d.store.ListIgnoredKeys(ctx, filter.MachineID)
	if err != nil {
		return GapReport{}, err
	}
	report := Gaps(declared, containers, checks, ignored)
	if filter.ProjectID != "" {
		report.Undeclared = []Undeclared{}
	}
	return report, nil
}

// observed lit les conteneurs vivants et les constats d'images ; sans
// source branchée, la machine ne montre rien.
func (d *Directory) observed(ctx context.Context, machineID string) ([]container.Container, map[string]update.Check, error) {
	if d.containers == nil || d.checks == nil {
		return nil, nil, nil
	}
	containers, err := d.containers.List(ctx, machineID)
	if err != nil {
		return nil, nil, err
	}
	checks, err := d.checks.Checks(ctx, machineID)
	if err != nil {
		return nil, nil, err
	}
	return containers, checks, nil
}

// Propose tire de la fiche d'un conteneur non déclaré la définition à
// relire avant d'adopter. Un conteneur inconnu ou détruit est introuvable.
func (d *Directory) Propose(ctx context.Context, containerID string) (Proposal, error) {
	if d.containers == nil {
		return Proposal{}, ErrContainerNotFound
	}
	item, err := d.containers.Get(ctx, containerID)
	if errors.Is(err, container.ErrNotFound) {
		return Proposal{}, ErrContainerNotFound
	}
	if err != nil {
		return Proposal{}, err
	}
	if item.IsArchived() {
		return Proposal{}, ErrContainerNotFound
	}
	siblings, err := d.containers.List(ctx, item.MachineID)
	if err != nil {
		return Proposal{}, err
	}
	declared, err := d.store.ListDeclaredServices(ctx, Filter{MachineID: item.MachineID})
	if err != nil {
		return Proposal{}, err
	}
	return Propose(item, siblings, declared)
}

// Proposals tire de ce que les machines montrent sans le déclarer une
// proposition par groupe que l'opérateur n'ignore pas. Rien n'est écrit : le
// fichier de l'export les propose à la relecture. Sans conteneurs branchés,
// il n'y a rien à proposer.
func (d *Directory) Proposals(ctx context.Context) ([]Proposal, error) {
	if d.containers == nil {
		return nil, nil
	}
	declared, err := d.store.ListDeclaredServices(ctx, Filter{})
	if err != nil {
		return nil, err
	}
	containers, err := d.containers.List(ctx, "")
	if err != nil {
		return nil, err
	}
	ignored, err := d.store.ListIgnoredKeys(ctx, "")
	if err != nil {
		return nil, err
	}
	return ProposeAll(declared, containers, ignored), nil
}

// Ignore dit que ce qui est présent sans être déclaré, c'est voulu. Le dire
// deux fois est sans effet. Cela ne change aucun rattachement.
func (d *Directory) Ignore(ctx context.Context, key IgnoredKey) error {
	key = key.Complete()
	if err := key.Validate(); err != nil {
		return err
	}
	key.CreatedAt = d.now()
	if err := d.store.InsertIgnoredKey(ctx, key); err != nil {
		return err
	}
	d.logger.Info("key ignored", "machine_id", key.MachineID, "kind", key.Kind, "value", key.Value)
	d.changed()
	return nil
}

func (d *Directory) Unignore(ctx context.Context, key IgnoredKey) error {
	key = key.Complete()
	if err := d.store.DeleteIgnoredKey(ctx, key); err != nil {
		return err
	}
	d.logger.Info("key no longer ignored", "machine_id", key.MachineID, "kind", key.Kind, "value", key.Value)
	d.changed()
	return nil
}
