package directory

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/registry"
)

// Proposal est ce que « Accepter » écrit, et que « Modifier avant d'accepter »
// pré-remplit dans l'assistant : une déclaration nouvelle. Rien n'est écrit
// tant que l'opérateur n'a pas accepté.
type Proposal struct {
	Definition ServiceDefinition
}

// Propose lit un conteneur non déclaré et en tire une définition que
// l'opérateur n'a plus qu'à relire : un service Compose est un service
// (traefik, authelia, crowdsec d'un même projet sont trois propositions), un
// conteneur seul est un service. Des répliques d'un service Compose ne font
// qu'une unité. Le projet Compose reste le groupe d'exécution, jamais un
// service de plus.
//
// Un conteneur qui porte des labels n'a pas de proposition : il n'est non
// déclaré que parce que ses labels ne visent aucune unité de sa machine, et
// l'unité qu'on lui créerait ne le rattacherait pas (ErrContainerLabelled).
func Propose(item container.Container, declared []Service) (Proposal, error) {
	if item.UnitID != "" {
		return Proposal{}, ErrContainerDeclared
	}
	if !item.Labels.IsEmpty() {
		return Proposal{}, ErrContainerLabelled
	}
	if item.Group == "" || item.ComposeService == "" {
		return proposeContainer(item, declared), nil
	}
	return proposeComposeService(item, declared), nil
}

// ProposeAll propose une définition par service Compose ou par conteneur seul
// non déclaré que l'opérateur n'ignore pas, sans rien écrire : export
// -propose l'écrit dans le fichier ; une découverte, si elle vient un jour,
// partira d'ici.
//
// Le représentant d'un groupe est son premier conteneur sans label ; un
// groupe où tous en portent est sauté. Chaque nouvelle définition entre dans
// une copie des services déclarés, pour que deux groupes ne reçoivent jamais
// le même nom sur une machine. Le résultat suit l'ordre des groupes de
// l'écart : le même état donne les mêmes propositions.
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
		proposal, err := Propose(representative, known)
		if err != nil {
			// Les refus ne peuvent pas arriver ici : le représentant n'a ni
			// unité ni label.
			continue
		}
		known = append(known, Service{
			MachineID: proposal.Definition.MachineID, Name: proposal.Definition.Name,
			Origin: proposal.Definition.Origin, ComposeProject: proposal.Definition.ComposeProject,
		})
		proposals = append(proposals, proposal)
	}
	return proposals
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
			DataPath: unit.DataPath, UpdatePolicy: unit.UpdatePolicy,
		})
	}
	return ServiceDefinition{
		MachineID:      service.MachineID,
		Name:           service.Name,
		Title:          service.Title,
		Nature:         service.Nature,
		Category:       service.Category,
		Origin:         service.Origin,
		ComposeProject: service.ComposeProject,
		PublicURL:      service.PublicURL,
		Provides:       slices.Clone(service.Provides),
		Units:          units,
		ProjectIDs:     slices.Clone(service.ProjectIDs),
	}
}

func proposeComposeService(item container.Container, declared []Service) Proposal {
	unit := unitProposal(item)
	unit.Name = item.ComposeService
	return Proposal{Definition: ServiceDefinition{
		MachineID:      item.MachineID,
		Name:           composeServiceName(item, declared),
		Nature:         NatureApplication,
		Origin:         OriginCompose,
		ComposeProject: item.Group,
		Units:          []UnitDefinition{unit},
	}}
}

// composeServiceName nomme le service comme son service Compose ; si la
// machine a déjà un service de ce nom (un « db » d'un autre projet), le nom
// du projet le précède.
func composeServiceName(item container.Container, declared []Service) string {
	plain := slug(item.ComposeService)
	if !isNameTaken(plain, item.MachineID, declared) {
		return plain
	}
	return freeName(slug(item.Group+"-"+item.ComposeService), item.MachineID, declared)
}

func isNameTaken(name, machineID string, declared []Service) bool {
	return slices.ContainsFunc(declared, func(existing Service) bool {
		return existing.MachineID == machineID && existing.Name == name
	})
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
