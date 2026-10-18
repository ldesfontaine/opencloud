package directory

import (
	"slices"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/service"
)

// Match rattache les conteneurs vivants aux unités déclarées. La règle est
// pure : l'annuaire d'un côté, ce que la machine montre de l'autre, la
// réponse par identifiant de fiche. Une fiche absente du résultat est
// « non déclarée ».
//
// Deux niveaux, disjoints par l'origine du service :
//   - compose : le projet Compose et le service Compose du conteneur sont
//     ceux du service et de l'unité déclarés ;
//   - name (faible) : le nom du conteneur est celui d'une unité d'un service
//     déclaré « conteneur seul ».
//
// Le projet gagne si les deux s'appliquent, ce qui n'arrive qu'à un
// conteneur Compose dont le nom est aussi déclaré par un service
// « conteneur seul » : celui-ci paraît alors « déclaré absent ». Plusieurs
// conteneurs d'une même unité sont admis (répliques). Une unité binaire ne
// rapproche jamais rien. Un conteneur archivé n'est pas considéré : il
// garde le rattachement qu'il avait.
func Match(declared []Service, containers []service.Service) map[string]service.UnitMatch {
	byCompose, byName := indexUnits(declared)
	matches := make(map[string]service.UnitMatch, len(containers))
	for _, container := range containers {
		if container.IsArchived() {
			continue
		}
		if match, ok := matchCompose(byCompose, container); ok {
			matches[container.ID] = match
			continue
		}
		if match, ok := matchName(byName, container); ok {
			matches[container.ID] = match
		}
	}
	return matches
}

func matchCompose(byCompose map[string]string, container service.Service) (service.UnitMatch, bool) {
	if container.Group == "" || container.ComposeService == "" {
		return service.UnitMatch{}, false
	}
	unitID, ok := byCompose[composeKey(container.MachineID, container.Group, container.ComposeService)]
	return service.UnitMatch{UnitID: unitID, Level: service.MatchCompose}, ok
}

func matchName(byName map[string]string, container service.Service) (service.UnitMatch, bool) {
	if container.Name == "" {
		return service.UnitMatch{}, false
	}
	unitID, ok := byName[nameKey(container.MachineID, container.Name)]
	return service.UnitMatch{UnitID: unitID, Level: service.MatchName}, ok
}

// indexUnits range les unités par clé de rapprochement. Les index uniques
// de la base empêchent deux unités d'avoir la même clé ; par prudence, si
// cela arrivait, le service à l'identifiant le plus petit gagnerait, puis
// la première unité dans l'ordre du service.
func indexUnits(declared []Service) (byCompose, byName map[string]string) {
	ordered := slices.SortedFunc(slices.Values(declared), func(a, b Service) int {
		return strings.Compare(a.ID, b.ID)
	})
	byCompose = map[string]string{}
	byName = map[string]string{}
	for _, declaredService := range ordered {
		for _, unit := range declaredService.Units {
			switch declaredService.Origin {
			case OriginCompose:
				keepFirst(byCompose, composeKey(declaredService.MachineID, declaredService.ComposeProject, unit.Name), unit.ID)
			case OriginContainer:
				keepFirst(byName, nameKey(declaredService.MachineID, unit.ContainerName), unit.ID)
			case OriginBinary:
				// Un binaire ne se retrouve pas parmi les conteneurs.
			}
		}
	}
	return byCompose, byName
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
