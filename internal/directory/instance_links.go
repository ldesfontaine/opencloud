package directory

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/link"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

var (
	ErrLinkServiceInvalid = refusal.Invalid("link.service_invalid", "link service must exist")
	ErrLinkUnitRequired   = refusal.Invalid("link.unit_required", "link service has several units, or none that can be found: say which")
)

// Linker est le composant des liens : l'annuaire lui donne des extrémités
// qu'il sait nommer (une unité d'un service), il garde les liens. nil est
// toléré.
type Linker interface {
	Declare(ctx context.Context, definition link.Definition) (link.Link, error)
	Remove(ctx context.Context, id string) error
	List(ctx context.Context, machineID string) ([]link.Link, error)
	ConfirmRoute(ctx context.Context, definition link.Definition) (link.Link, error)
	DismissRoute(ctx context.Context, definition link.Definition) (link.Link, error)
	Restore(ctx context.Context, id string) error
	ListDismissed(ctx context.Context, machineID string) ([]link.Link, error)
}

var errNoLinker = errors.New("links are not wired")

// SetLinker branche les liens : sans lui, aucun service n'a de lien.
func (d *Directory) SetLinker(linker Linker) {
	d.linker = linker
}

// UnitKey est la clé d'instance d'une unité, celle que les conteneurs et les
// unités systemd relevés portent : « compose:projet/service », « container:nom »,
// « systemd:unité ». Vide si l'unité ne dit pas encore qui elle est.
func UnitKey(owner Service, unit Unit) string {
	switch owner.Origin {
	case OriginCompose:
		if owner.ComposeProject == "" || unit.Name == "" {
			return ""
		}
		return link.ComposeKey(owner.ComposeProject, unit.Name)
	case OriginContainer:
		if unit.ContainerName == "" {
			return ""
		}
		return link.ContainerKey(unit.ContainerName)
	default:
		if unit.SystemdUnit == "" {
			return ""
		}
		return link.SystemdKey(unit.SystemdUnit)
	}
}

// LinkEnd : l'extrémité d'un lien vue de l'annuaire. Service et unité sont
// vides quand aucun service déclaré ne porte cette instance.
type LinkEnd struct {
	MachineID   string
	Key         string
	ServiceID   string
	ServiceName string
	UnitName    string
}

// ServiceLink est un lien lu depuis un service : de son côté (Self) vers
// l'autre (Peer). Outgoing dit si le service est la source. Dismissed : une
// route déduite que l'opérateur a écartée, gardée pour pouvoir la rétablir.
type ServiceLink struct {
	Link      link.Link
	Outgoing  bool
	Dismissed bool
	Self      LinkEnd
	Peer      LinkEnd
}

// LinkDefinition : ce que l'opérateur donne. Chaque extrémité est un service
// et, s'il en a plusieurs, l'unité visée ; une seule unité se choisit seule.
type LinkDefinition struct {
	FromServiceID string
	FromUnitID    string
	ToServiceID   string
	ToUnitID      string
	Port          int
}

// DeclareLink écrit le lien que l'opérateur décrit entre deux unités.
func (d *Directory) DeclareLink(ctx context.Context, definition LinkDefinition) (link.Link, error) {
	if d.linker == nil {
		return link.Link{}, errNoLinker
	}
	from, err := d.linkEndpoint(ctx, definition.FromServiceID, definition.FromUnitID)
	if err != nil {
		return link.Link{}, err
	}
	to, err := d.linkEndpoint(ctx, definition.ToServiceID, definition.ToUnitID)
	if err != nil {
		return link.Link{}, err
	}
	return d.linker.Declare(ctx, link.Definition{From: from, To: to, Port: definition.Port})
}

// linkEndpoint rend l'instance d'une unité de service. Sans unité nommée, le
// service doit n'en avoir qu'une : sinon le choix est à l'opérateur.
func (d *Directory) linkEndpoint(ctx context.Context, serviceID, unitID string) (link.Endpoint, error) {
	owner, err := d.store.GetDeclaredService(ctx, serviceID)
	if errors.Is(err, ErrNotFound) {
		return link.Endpoint{}, ErrLinkServiceInvalid
	}
	if err != nil {
		return link.Endpoint{}, fmt.Errorf("get link service: %w", err)
	}
	var chosen Unit
	switch {
	case unitID != "":
		index := slices.IndexFunc(owner.Units, func(unit Unit) bool { return unit.ID == unitID })
		if index < 0 {
			return link.Endpoint{}, ErrUnitInvalid
		}
		chosen = owner.Units[index]
	case len(owner.Units) == 1:
		chosen = owner.Units[0]
	default:
		return link.Endpoint{}, ErrLinkUnitRequired
	}
	key := UnitKey(owner, chosen)
	if key == "" {
		return link.Endpoint{}, ErrLinkUnitRequired
	}
	return link.Endpoint{MachineID: owner.MachineID, Key: key}, nil
}

// RemoveLink retire un lien que l'opérateur a écrit.
func (d *Directory) RemoveLink(ctx context.Context, id string) error {
	if d.linker == nil {
		return errNoLinker
	}
	return d.linker.Remove(ctx, id)
}

// ServiceLinks rend les liens des instances d'un service, entrants et
// sortants, chaque extrémité nommée par le service déclaré qui la porte
// quand il y en a un. Viennent d'abord les liens écrits, puis les routes
// déduites à confirmer, puis celles que l'opérateur a écartées. Un lien d'une
// unité du service vers une autre unité du même service est rendu une fois,
// sortant.
func (d *Directory) ServiceLinks(ctx context.Context, serviceID string) ([]ServiceLink, error) {
	owner, err := d.store.GetDeclaredService(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	if d.linker == nil {
		return []ServiceLink{}, nil
	}
	stored, err := d.linker.List(ctx, owner.MachineID)
	if err != nil {
		return nil, err
	}
	dismissed, err := d.linker.ListDismissed(ctx, owner.MachineID)
	if err != nil {
		return nil, err
	}
	declared, err := d.store.ListDeclaredServices(ctx, Filter{})
	if err != nil {
		return nil, err
	}
	deduced, err := d.deducedRoutes(ctx, declared)
	if err != nil {
		return nil, err
	}
	ends := indexLinkEnds(declared)
	own := map[link.Endpoint]bool{}
	for _, unit := range owner.Units {
		if key := UnitKey(owner, unit); key != "" {
			own[link.Endpoint{MachineID: owner.MachineID, Key: key}] = true
		}
	}
	links := []ServiceLink{}
	place := func(found link.Link, isDismissed bool) {
		switch {
		case own[found.From]:
			links = append(links, ServiceLink{Link: found, Outgoing: true, Dismissed: isDismissed, Self: ends.of(found.From), Peer: ends.of(found.To)})
		case own[found.To]:
			links = append(links, ServiceLink{Link: found, Dismissed: isDismissed, Self: ends.of(found.To), Peer: ends.of(found.From)})
		}
	}
	for _, found := range stored {
		place(found, false)
	}
	for _, found := range deduced {
		place(found, false)
	}
	for _, found := range dismissed {
		place(found, true)
	}
	return links, nil
}

type linkEnds map[link.Endpoint]LinkEnd

// of rend l'extrémité nommée, ou la clé nue quand aucun service ne la porte.
func (e linkEnds) of(endpoint link.Endpoint) LinkEnd {
	if found, ok := e[endpoint]; ok {
		return found
	}
	return LinkEnd{MachineID: endpoint.MachineID, Key: endpoint.Key}
}

func indexLinkEnds(declared []Service) linkEnds {
	ordered := slices.Clone(declared)
	slices.SortFunc(ordered, func(a, b Service) int { return strings.Compare(a.ID, b.ID) })
	ends := linkEnds{}
	for _, owner := range ordered {
		for _, unit := range owner.Units {
			key := UnitKey(owner, unit)
			if key == "" {
				continue
			}
			endpoint := link.Endpoint{MachineID: owner.MachineID, Key: key}
			if _, taken := ends[endpoint]; !taken {
				ends[endpoint] = LinkEnd{MachineID: owner.MachineID, Key: key, ServiceID: owner.ID, ServiceName: owner.Name, UnitName: unit.Name}
			}
		}
	}
	return ends
}
