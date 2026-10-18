package directory

import (
	"slices"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/container"
)

// Match rattache les conteneurs vivants aux unités déclarées. La règle est
// pure : l'annuaire d'un côté, ce que la machine montre de l'autre, la
// réponse par identifiant de fiche. Une fiche absente du résultat est
// « non déclarée ».
//
// Trois niveaux, du plus fort au plus faible :
//   - label : le conteneur porte les deux labels de la marque, l'identifiant
//     d'un service déclaré sur sa machine et le nom d'une de ses unités
//     conteneur. Un conteneur qui porte un label au moins ne cherche que
//     par là : si les labels ne visent aucune unité de sa machine, il est
//     « non déclaré » (voir LabelProblemOf), sans repli sur les deux autres
//     niveaux. Une affirmation fausse doit se voir ;
//   - compose : le projet Compose et le service Compose du conteneur sont
//     ceux du service et de l'unité déclarés ;
//   - name (faible) : le nom du conteneur est celui d'une unité d'un service
//     déclaré « conteneur seul ».
//
// Sans label, le projet gagne si les deux derniers s'appliquent, ce qui
// n'arrive qu'à un conteneur Compose dont le nom est aussi déclaré par un
// service « conteneur seul » : celui-ci paraît alors « déclaré absent ».
// Plusieurs conteneurs d'une même unité sont admis (répliques). Une unité
// binaire ne rapproche jamais rien. Un conteneur archivé n'est pas considéré :
// il garde le rattachement qu'il avait.
//
// Un label ne fait que chercher : il ne crée ni ne modifie jamais rien dans
// l'annuaire, et la machine visée est toujours celle du conteneur.
func Match(declared []Service, containers []container.Container) map[string]container.UnitMatch {
	index := indexUnits(declared)
	matches := make(map[string]container.UnitMatch, len(containers))
	for _, item := range containers {
		if item.IsArchived() {
			continue
		}
		if match, ok := matchOne(index, item); ok {
			matches[item.ID] = match
		}
	}
	return matches
}

// labelKey est la clé du niveau label. Une clé structurée et non une
// concaténation : la valeur d'un label est libre, et un espace dedans ne doit
// jamais faire passer un conteneur pour celui d'une autre machine.
type labelKey struct {
	machineID string
	serviceID string
	unit      string
}

// unitIndex range les unités déclarées par niveau de rapprochement.
type unitIndex struct {
	byLabel   map[labelKey]string
	byCompose map[string]string
	byName    map[string]string
}

// matchOne applique la règle à une fiche, vivante ou non : un conteneur
// étiqueté ne cherche que par son label, sinon le projet gagne sur le nom.
// Match n'y met que le filtre « vivant » ; la reprise de l'existant s'en sert
// aussi pour les fiches archivées.
func matchOne(index unitIndex, item container.Container) (container.UnitMatch, bool) {
	if !item.Labels.IsEmpty() {
		return matchLabel(index.byLabel, item)
	}
	if match, ok := matchCompose(index.byCompose, item); ok {
		return match, true
	}
	return matchName(index.byName, item)
}

func matchLabel(byLabel map[labelKey]string, item container.Container) (container.UnitMatch, bool) {
	if item.Labels.Invalid || item.Labels.Service == "" || item.Labels.Unit == "" {
		return container.UnitMatch{}, false
	}
	unitID, ok := byLabel[labelKey{machineID: item.MachineID, serviceID: item.Labels.Service, unit: item.Labels.Unit}]
	return container.UnitMatch{UnitID: unitID, Level: container.MatchLabel}, ok
}

func matchCompose(byCompose map[string]string, item container.Container) (container.UnitMatch, bool) {
	if item.Group == "" || item.ComposeService == "" {
		return container.UnitMatch{}, false
	}
	unitID, ok := byCompose[composeKey(item.MachineID, item.Group, item.ComposeService)]
	return container.UnitMatch{UnitID: unitID, Level: container.MatchCompose}, ok
}

func matchName(byName map[string]string, item container.Container) (container.UnitMatch, bool) {
	if item.Name == "" {
		return container.UnitMatch{}, false
	}
	unitID, ok := byName[nameKey(item.MachineID, item.Name)]
	return container.UnitMatch{UnitID: unitID, Level: container.MatchName}, ok
}

// indexUnits range les unités par clé de rapprochement. Les index uniques
// de la base empêchent deux unités d'avoir la même clé ; par prudence, si
// cela arrivait, le service à l'identifiant le plus petit gagnerait, puis
// la première unité dans l'ordre du service. Le niveau label se range sous
// la machine du service déclaré : celle que cherche un conteneur est la
// sienne, jamais une autre.
func indexUnits(declared []Service) unitIndex {
	ordered := slices.SortedFunc(slices.Values(declared), func(a, b Service) int {
		return strings.Compare(a.ID, b.ID)
	})
	index := unitIndex{byLabel: map[labelKey]string{}, byCompose: map[string]string{}, byName: map[string]string{}}
	for _, declaredService := range ordered {
		for _, unit := range declaredService.Units {
			switch declaredService.Origin {
			case OriginCompose:
				keepFirst(index.byCompose, composeKey(declaredService.MachineID, declaredService.ComposeProject, unit.Name), unit.ID)
			case OriginContainer:
				keepFirst(index.byName, nameKey(declaredService.MachineID, unit.ContainerName), unit.ID)
			case OriginBinary:
				// Un binaire ne se retrouve pas parmi les conteneurs.
			}
			if declaredService.Origin != OriginBinary {
				key := labelKey{machineID: declaredService.MachineID, serviceID: declaredService.ID, unit: unit.Name}
				if _, taken := index.byLabel[key]; !taken {
					index.byLabel[key] = unit.ID
				}
			}
		}
	}
	return index
}

func keepFirst(index map[string]string, key, unitID string) {
	if _, taken := index[key]; !taken {
		index[key] = unitID
	}
}

// Une machine, un projet, un service Compose : l'espace sépare des valeurs
// qui n'en portent jamais.
func composeKey(machineID, project, composeService string) string {
	return machineID + " " + project + " " + composeService
}

func nameKey(machineID, containerName string) string {
	return machineID + " " + containerName
}

// LabelProblem dit pourquoi les labels d'un conteneur ne le rattachent à
// rien ; vide s'il n'en porte pas ou s'ils visent une unité conteneur d'un
// service de sa machine.
type LabelProblem string

const (
	LabelOK LabelProblem = ""
	// Des labels de la marque que l'agent ou le serveur ont dû écarter (clé en
	// majuscules, valeur trop longue ou illisible…) : le conteneur est étiqueté,
	// mal, et ne retombe sur rien.
	LabelInvalid LabelProblem = "invalid"
	// Un seul des deux labels : il faut le service et l'unité.
	LabelIncomplete LabelProblem = "incomplete"
	// Aucun service de cet identifiant sur la machine du conteneur : inconnu,
	// supprimé, mal écrit, ou déclaré sur une autre machine.
	LabelServiceUnknown LabelProblem = "service_unknown"
	LabelUnitUnknown    LabelProblem = "unit_unknown"
	// L'unité visée est un binaire : aucun conteneur ne s'y rattache.
	LabelUnitBinary LabelProblem = "unit_binary"
)

// LabelProblems liste les motifs ; le vide, des labels sans défaut, n'en est
// pas un.
func LabelProblems() []LabelProblem {
	return []LabelProblem{LabelInvalid, LabelIncomplete, LabelServiceUnknown, LabelUnitUnknown, LabelUnitBinary}
}

// LabelProblemOf dit pourquoi les labels du conteneur ne le rattachent à
// rien. L'ordre est celui de la recherche : des labels lisibles, les deux labels, le service sur
// la machine du conteneur, l'unité dans ce service, puis son genre. Il suit
// la règle de matchLabel : le motif est vide si et seulement si le conteneur
// se rattache par son label.
func LabelProblemOf(declared []Service, item container.Container) LabelProblem {
	if item.Labels.IsEmpty() {
		return LabelOK
	}
	if item.Labels.Invalid {
		return LabelInvalid
	}
	if item.Labels.Service == "" || item.Labels.Unit == "" {
		return LabelIncomplete
	}
	for _, candidate := range declared {
		if candidate.ID != item.Labels.Service || candidate.MachineID != item.MachineID {
			continue
		}
		for _, unit := range candidate.Units {
			if unit.Name != item.Labels.Unit {
				continue
			}
			if candidate.Origin == OriginBinary {
				return LabelUnitBinary
			}
			return LabelOK
		}
		return LabelUnitUnknown
	}
	return LabelServiceUnknown
}
