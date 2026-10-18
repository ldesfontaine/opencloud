package directory

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/registry"
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
//
// Un conteneur qui porte des labels n'est pas adoptable : il n'est non
// déclaré que parce que ses labels ne visent aucune unité de sa machine, et
// l'unité qu'on lui créerait ne le rattacherait pas (ErrContainerLabelled).
// Ses voisins étiquetés sont laissés de côté pour la même raison.
func Propose(item container.Container, siblings []container.Container, declared []Service) (Proposal, error) {
	if item.UnitID != "" {
		return Proposal{}, ErrContainerDeclared
	}
	if !item.Labels.IsEmpty() {
		return Proposal{}, ErrContainerLabelled
	}
	if item.Group == "" || item.ComposeService == "" {
		return proposeContainer(item, declared), nil
	}
	missing := composeUnitsToAdopt(item, siblings)
	for _, existing := range declared {
		if existing.Origin == OriginCompose && existing.MachineID == item.MachineID && existing.ComposeProject == item.Group {
			return proposeIntoService(existing, item, missing)
		}
	}
	return proposeComposeProject(item, requestedFirst(missing, item, MaxUnits), declared), nil
}

// requestedFirst met l'unité du conteneur demandé devant. La coupe à
// MaxUnits tombe sur la fin de la liste triée : elle ne doit pas emporter ce
// sur quoi l'opérateur a cliqué. Sans coupe, l'ordre par nom reste celui que
// l'opérateur retrouvera dans la fiche.
func requestedFirst(units []container.Container, item container.Container, room int) []container.Container {
	if len(units) <= room {
		return units
	}
	at := slices.IndexFunc(units, func(candidate container.Container) bool { return candidate.ComposeService == item.ComposeService })
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
func composeUnitsToAdopt(item container.Container, siblings []container.Container) []container.Container {
	candidates := []container.Container{item}
	for _, sibling := range siblings {
		isSameProject := sibling.MachineID == item.MachineID && sibling.Group == item.Group
		if isSameProject && sibling.ID != item.ID && sibling.UnitID == "" && !sibling.IsArchived() && sibling.ComposeService != "" && sibling.Labels.IsEmpty() {
			candidates = append(candidates, sibling)
		}
	}
	candidates = sortedByName(candidates)
	byService := map[string]container.Container{}
	for _, candidate := range candidates {
		if _, seen := byService[candidate.ComposeService]; !seen {
			byService[candidate.ComposeService] = candidate
		}
	}
	units := make([]container.Container, 0, len(byService))
	for _, found := range byService {
		units = append(units, found)
	}
	slices.SortFunc(units, func(a, b container.Container) int { return strings.Compare(a.ComposeService, b.ComposeService) })
	return units
}

// ProposeAll propose une définition par groupe de conteneurs non déclarés
// que l'opérateur n'ignore pas, sans rien écrire : export -propose l'écrit
// dans le fichier ; une découverte, si elle vient un jour, partira d'ici.
//
// Le représentant d'un groupe est son premier conteneur sans label ; un
// groupe où tous en portent est sauté. Chaque nouvelle définition entre dans
// une copie des services déclarés, pour que deux groupes ne reçoivent jamais
// le même nom sur une machine. Un service déjà plein saute son groupe. Le
// résultat suit l'ordre des groupes de l'écart : le même état donne les mêmes
// propositions.
func ProposeAll(declared []Service, containers []container.Container, ignored []IgnoredKey) []Proposal {
	living := make([]container.Container, 0, len(containers))
	for _, item := range containers {
		if !item.IsArchived() {
			living = append(living, item)
		}
	}
	known := slices.Clone(declared)
	var proposals []Proposal
	for _, group := range undeclaredOf(declared, living, ignored) {
		if group.Ignored || !group.Adoptable {
			continue
		}
		representative := group.Containers[slices.IndexFunc(group.Containers, func(item container.Container) bool { return item.Labels.IsEmpty() })]
		proposal, err := Propose(representative, living, known)
		if err != nil {
			// Un service plein (ErrUnitsFull) ne reçoit plus rien ; les autres
			// refus ne peuvent pas arriver ici, le représentant n'a ni unité
			// ni label.
			continue
		}
		if proposal.ServiceID == "" {
			known = append(known, Service{
				MachineID: proposal.Definition.MachineID, Name: proposal.Definition.Name,
				Origin: proposal.Definition.Origin, ComposeProject: proposal.Definition.ComposeProject,
			})
		}
		proposals = append(proposals, proposal)
	}
	return proposals
}

func proposeIntoService(existing Service, requested container.Container, missing []container.Container) (Proposal, error) {
	if len(existing.Units) >= MaxUnits {
		return Proposal{}, ErrUnitsFull
	}
	definition := existing.Definition()
	for _, item := range requestedFirst(missing, requested, MaxUnits-len(existing.Units)) {
		if len(definition.Units) >= MaxUnits {
			break
		}
		if slices.ContainsFunc(definition.Units, func(unit UnitDefinition) bool { return unit.Name == item.ComposeService }) {
			continue
		}
		unit := unitProposal(item)
		unit.Name = item.ComposeService
		definition.Units = append(definition.Units, unit)
	}
	return Proposal{ServiceID: existing.ID, Definition: definition}, nil
}

func proposeComposeProject(item container.Container, missing []container.Container, declared []Service) Proposal {
	definition := ServiceDefinition{
		MachineID:      item.MachineID,
		Name:           freeName(slug(item.Group), item.MachineID, declared),
		Nature:         NatureApplication,
		Origin:         OriginCompose,
		ComposeProject: item.Group,
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

func proposeContainer(item container.Container, declared []Service) Proposal {
	unit := unitProposal(item)
	unit.Name = unitNameOf(item.Name)
	unit.ContainerName = item.Name
	return Proposal{Definition: ServiceDefinition{
		MachineID: item.MachineID,
		Name:      freeName(slug(item.Name), item.MachineID, declared),
		Nature:    NatureApplication,
		Origin:    OriginContainer,
		Units:     []UnitDefinition{unit},
	}}
}

// Definition redonne un service déclaré sous la forme que l'édition remplace
// entière : le fichier de l'annuaire s'en sert pour comparer ce qu'il demande
// à ce qui est déclaré.
func (service Service) Definition() ServiceDefinition {
	units := make([]UnitDefinition, 0, len(service.Units))
	for _, unit := range service.Units {
		units = append(units, UnitDefinition{
			ID: unit.ID, Name: unit.Name, Kind: unit.Kind, Image: unit.Image, Version: unit.Version, Digest: unit.Digest,
			ContainerName: unit.ContainerName, SystemdUnit: unit.SystemdUnit, Ports: slices.Clone(unit.Ports),
			UpdatePolicy: unit.UpdatePolicy,
		})
	}
	return ServiceDefinition{
		MachineID:      service.MachineID,
		Name:           service.Name,
		Title:          service.Title,
		Nature:         service.Nature,
		Origin:         service.Origin,
		ComposeProject: service.ComposeProject,
		PublicURL:      service.PublicURL,
		Provides:       slices.Clone(service.Provides),
		Units:          units,
		ProjectIDs:     slices.Clone(service.ProjectIDs),
	}
}

// unitProposal tire d'un conteneur ce que la déclaration d'une unité saurait
// dire de lui : l'image sans tag ni empreinte, la version, les ports. Ni
// « latest » ni une empreinte lue après coup : la déclaration ne dit que ce
// que l'image écrit.
func unitProposal(item container.Container) UnitDefinition {
	unit := UnitDefinition{Kind: UnitContainer, Image: item.Image, Ports: portsOf(item.Ports)}
	reference, err := registry.Parse(item.Image)
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
func portsOf(published []container.Port) []string {
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
