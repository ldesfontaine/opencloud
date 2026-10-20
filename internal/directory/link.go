package directory

import (
	"context"
	"errors"

	"github.com/ldesfontaine/opencloud/internal/container"
)

// ContainerLink est ce que la fiche d'un conteneur dit de l'annuaire :
// l'unité à laquelle il se rattache, ou, sans elle, la clé par laquelle on
// l'ignorerait.
type ContainerLink struct {
	ServiceID   string
	ServiceName string
	UnitID      string
	UnitName    string
	Level       container.MatchLevel
	Key         IgnoredKey
	Ignored     bool
	// Les labels de la marque que le conteneur porte, et, s'il n'est pas
	// rattaché, pourquoi ils ne le rattachent à rien. LabelMachineName est le
	// nom de la machine qui déclare le service visé quand c'en est une autre :
	// un label ne rattache jamais qu'un conteneur de sa propre machine.
	Labels           container.Labels
	LabelProblem     LabelProblem
	LabelMachineName string
}

func (d *Directory) ContainerLink(ctx context.Context, item container.Container) (ContainerLink, error) {
	link := ContainerLink{Key: keyOf(item), Labels: item.Labels}
	if item.UnitID != "" {
		return d.linkToUnit(ctx, item, link)
	}
	if err := d.explainLabels(ctx, item, &link); err != nil {
		return ContainerLink{}, err
	}
	ignored, err := d.store.ListIgnoredKeys(ctx, item.MachineID)
	if err != nil {
		return ContainerLink{}, err
	}
	link.Ignored = isIgnored(ignored, link.Key)
	return link, nil
}

// explainLabels dit pourquoi les labels d'un conteneur non rattaché ne le
// rattachent à rien, et, quand ils visent un service d'une autre machine, où
// il est déclaré. Il ne lit que : un label n'écrit jamais dans l'annuaire.
func (d *Directory) explainLabels(ctx context.Context, item container.Container, link *ContainerLink) error {
	if item.Labels.IsEmpty() {
		return nil
	}
	onMachine, err := d.store.ListDeclaredServices(ctx, Filter{MachineID: item.MachineID})
	if err != nil {
		return err
	}
	link.LabelProblem = LabelProblemOf(onMachine, item)
	if link.LabelProblem != LabelServiceUnknown {
		return nil
	}
	elsewhere, err := d.store.GetDeclaredService(ctx, item.Labels.Service)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	link.LabelMachineName = elsewhere.MachineName
	return nil
}

// linkToUnit cherche le service qui porte l'unité sur toutes les machines :
// une fiche archivée garde son unité, et le service a pu changer de machine
// depuis.
func (d *Directory) linkToUnit(ctx context.Context, item container.Container, link ContainerLink) (ContainerLink, error) {
	declared, err := d.store.ListDeclaredServices(ctx, Filter{})
	if err != nil {
		return ContainerLink{}, err
	}
	for _, found := range declared {
		for _, unit := range found.Units {
			if unit.ID == item.UnitID {
				link.ServiceID, link.ServiceName = found.ID, found.Name
				link.UnitID, link.UnitName = unit.ID, unit.Name
				link.Level = item.MatchLevel
				return link, nil
			}
		}
	}
	return link, nil
}
