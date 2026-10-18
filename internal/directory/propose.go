package directory

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/registry"
	"github.com/ldesfontaine/opencloud/internal/service"
)

// Proposal est ce que « Adopter » pré-remplit dans le formulaire. Avec un
// ServiceID, c'est la modification d'un service déjà déclaré, à qui une
// unité s'ajoute ; sans, c'est une déclaration nouvelle. Rien n'est écrit
// tant que l'opérateur n'a pas validé.
type Proposal struct {
	ServiceID  string
	Definition ServiceDefinition
}

// Propose lit un conteneur non déclaré et ses voisins de la machine, et en
// tire une définition que l'opérateur n'a plus qu'à relire.
//
// Un projet Compose déjà déclaré garde sa définition, ids d'unités compris ;
// les unités ajoutées arrivent sans id, comme l'édition l'attend. Les
// unités sont plafonnées à MaxUnits : un projet plus gros s'adopte en
// plusieurs fois, l'unité du conteneur demandé toujours en tête de la
// coupe. Un service déjà plein ne peut plus rien recevoir : ErrUnitsFull.
func Propose(container service.Service, siblings []service.Service, declared []Service) (Proposal, error) {
	if container.UnitID != "" {
		return Proposal{}, ErrContainerDeclared
	}
	if container.Group == "" || container.ComposeService == "" {
		return proposeContainer(container, declared), nil
	}
	missing := composeUnitsToAdopt(container, siblings)
	for _, existing := range declared {
		if existing.Origin == OriginCompose && existing.MachineID == container.MachineID && existing.ComposeProject == container.Group {
			return proposeIntoService(existing, container, missing)
		}
	}
	return proposeComposeProject(container, requestedFirst(missing, container, MaxUnits), declared), nil
}

// requestedFirst met l'unité du conteneur demandé devant. La coupe à
// MaxUnits tombe sur la fin de la liste triée : elle ne doit pas emporter ce
// sur quoi l'opérateur a cliqué. Sans coupe, l'ordre par nom reste celui que
// l'opérateur retrouvera dans la fiche.
func requestedFirst(units []service.Service, container service.Service, room int) []service.Service {
	if len(units) <= room {
		return units
	}
	at := slices.IndexFunc(units, func(candidate service.Service) bool { return candidate.ComposeService == container.ComposeService })
	if at <= 0 {
		return units
	}
	reordered := slices.Clone(units)
	requested := reordered[at]
	reordered = slices.Delete(reordered, at, at+1)
	return slices.Insert(reordered, 0, requested)
}

// composeUnitsToAdopt rend, par service Compose, le conteneur du projet qui
// n'est rattaché à rien, par nom de service Compose ; le conteneur demandé y
// est toujours. Des répliques ne font qu'une unité.
func composeUnitsToAdopt(container service.Service, siblings []service.Service) []service.Service {
	candidates := []service.Service{container}
	for _, sibling := range siblings {
		isSameProject := sibling.MachineID == container.MachineID && sibling.Group == container.Group
		if isSameProject && sibling.ID != container.ID && sibling.UnitID == "" && !sibling.IsArchived() && sibling.ComposeService != "" {
			candidates = append(candidates, sibling)
		}
	}
	candidates = sortedByName(candidates)
	byService := map[string]service.Service{}
	for _, candidate := range candidates {
		if _, seen := byService[candidate.ComposeService]; !seen {
			byService[candidate.ComposeService] = candidate
		}
	}
	units := make([]service.Service, 0, len(byService))
	for _, found := range byService {
		units = append(units, found)
	}
	slices.SortFunc(units, func(a, b service.Service) int { return strings.Compare(a.ComposeService, b.ComposeService) })
	return units
}

func proposeIntoService(existing Service, requested service.Service, missing []service.Service) (Proposal, error) {
	if len(existing.Units) >= MaxUnits {
		return Proposal{}, ErrUnitsFull
	}
	definition := definitionOf(existing)
	for _, container := range requestedFirst(missing, requested, MaxUnits-len(existing.Units)) {
		if len(definition.Units) >= MaxUnits {
			break
		}
		if slices.ContainsFunc(definition.Units, func(unit UnitDefinition) bool { return unit.Name == container.ComposeService }) {
			continue
		}
		unit := unitProposal(container)
		unit.Name = container.ComposeService
		definition.Units = append(definition.Units, unit)
	}
	return Proposal{ServiceID: existing.ID, Definition: definition}, nil
}

func proposeComposeProject(container service.Service, missing []service.Service, declared []Service) Proposal {
	definition := ServiceDefinition{
		MachineID:      container.MachineID,
		Name:           freeName(slug(container.Group), container.MachineID, declared),
		Nature:         NatureApplication,
		Origin:         OriginCompose,
		ComposeProject: container.Group,
	}
	for _, found := range missing {
		if len(definition.Units) >= MaxUnits {
			break
		}
		unit := unitProposal(found)
		unit.Name = found.ComposeService
		definition.Units = append(definition.Units, unit)
	}
	return Proposal{Definition: definition}
}

func proposeContainer(container service.Service, declared []Service) Proposal {
	unit := unitProposal(container)
	unit.Name = unitNameOf(container.Name)
	unit.ContainerName = container.Name
	return Proposal{Definition: ServiceDefinition{
		MachineID: container.MachineID,
		Name:      freeName(slug(container.Name), container.MachineID, declared),
		Nature:    NatureApplication,
		Origin:    OriginContainer,
		Units:     []UnitDefinition{unit},
	}}
}

// definitionOf redonne un service déclaré sous la forme que l'édition
// remplace entière.
func definitionOf(existing Service) ServiceDefinition {
	units := make([]UnitDefinition, 0, len(existing.Units))
	for _, unit := range existing.Units {
		units = append(units, UnitDefinition{
			ID: unit.ID, Name: unit.Name, Kind: unit.Kind, Image: unit.Image, Version: unit.Version, Digest: unit.Digest,
			ContainerName: unit.ContainerName, SystemdUnit: unit.SystemdUnit, Ports: slices.Clone(unit.Ports),
		})
	}
	return ServiceDefinition{
		MachineID:      existing.MachineID,
		Name:           existing.Name,
		Title:          existing.Title,
		Nature:         existing.Nature,
		Origin:         existing.Origin,
		ComposeProject: existing.ComposeProject,
		PublicURL:      existing.PublicURL,
		Provides:       slices.Clone(existing.Provides),
		Units:          units,
		ProjectIDs:     slices.Clone(existing.ProjectIDs),
	}
}

// unitProposal tire d'un conteneur ce que la déclaration d'une unité saurait
// dire de lui : l'image sans tag ni empreinte, la version, les ports. Ni
// « latest » ni une empreinte lue après coup : la déclaration ne dit que ce
// que l'image écrit.
func unitProposal(container service.Service) UnitDefinition {
	unit := UnitDefinition{Kind: UnitContainer, Image: container.Image, Ports: portsOf(container.Ports)}
	reference, err := registry.Parse(container.Image)
	if err != nil {
		// L'opérateur corrigera l'image dans le formulaire.
		return unit
	}
	unit.Image = reference.Familiar()
	unit.Digest = reference.Digest
	if reference.Tag != "" && !strings.EqualFold(reference.Tag, registry.DefaultTag) {
		unit.Version = reference.Tag
	}
	return unit
}

// portsOf écrit les ports publiés comme une unité les déclare. Docker publie
// chaque port en IPv4 et en IPv6 : une seule ligne pour les deux.
func portsOf(published []service.Port) []string {
	ports := []string{}
	for _, port := range published {
		if port.ContainerPort < 1 {
			continue
		}
		text := fmt.Sprintf("%d", port.ContainerPort)
		if port.HostPort > 0 {
			text = fmt.Sprintf("%d:%d", port.HostPort, port.ContainerPort)
		}
		if port.Protocol == "udp" {
			text += "/udp"
		}
		if !slices.Contains(ports, text) && len(ports) < MaxPorts {
			ports = append(ports, text)
		}
	}
	return ports
}

var (
	outsideServiceName = regexp.MustCompile(`[^a-z0-9_-]`)
	outsideUnitName    = regexp.MustCompile(`[^A-Za-z0-9_.-]`)
)

// slug passe en minuscules, remplace ce qu'un nom de service refuse et coupe
// à 63 caractères ; le vide ou ce qui ne laisse rien devient « service ».
func slug(text string) string {
	name := outsideServiceName.ReplaceAllString(strings.ToLower(text), "-")
	name = strings.TrimLeft(name, "-_")
	if len(name) > 63 {
		name = name[:63]
	}
	if name == "" {
		return "service"
	}
	return name
}

// unitNameOf garde la casse du nom du conteneur, qu'une unité admet, mais
// remplace ce qu'un nom d'unité refuse ; le vide devient « unit ».
func unitNameOf(containerName string) string {
	name := outsideUnitName.ReplaceAllString(containerName, "-")
	name = strings.TrimLeft(name, "-_.")
	if len(name) > 63 {
		name = name[:63]
	}
	if name == "" {
		return "unit"
	}
	return name
}

// freeName cherche un nom que les services déclarés de la machine
// n'utilisent pas encore, pour que la proposition ne soit pas refusée à
// l'enregistrement (nom unique par machine) ; le suffixe reste dans les 63
// caractères.
func freeName(base, machineID string, declared []Service) string {
	taken := map[string]bool{}
	for _, existing := range declared {
		if existing.MachineID == machineID {
			taken[existing.Name] = true
		}
	}
	name := base
	for suffix := 2; taken[name]; suffix++ {
		tail := fmt.Sprintf("-%d", suffix)
		name = base[:min(len(base), 63-len(tail))] + tail
	}
	return name
}
