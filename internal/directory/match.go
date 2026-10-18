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
func Match(declared []Service, containers []container.Container) map[string]container.UnitMatch {
	byCompose, byName := indexUnits(declared)
	matches := make(map[string]container.UnitMatch, len(containers))
	for _, item := range containers {
		if item.IsArchived() {
			continue
		}
		if match, ok := matchOne(byCompose, byName, item); ok {
			matches[item.ID] = match
		}
	}
	return matches
}

// matchOne applique la règle à une fiche, vivante ou non : le projet gagne
// sur le nom. Match n'y met que le filtre « vivant » ; la reprise de
// l'existant s'en sert aussi pour les fiches archivées.
func matchOne(byCompose, byName map[string]string, item container.Container) (container.UnitMatch, bool) {
	if match, ok := matchCompose(byCompose, item); ok {
		return match, true
	}
	return matchName(byName, item)
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
