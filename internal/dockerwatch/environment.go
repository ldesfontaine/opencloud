package dockerwatch

import (
	"cmp"
	"slices"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/dockerapi"
)

// La liste blanche de l'environnement (étude services-modèle §2.4, « liste
// blanche par résolution, pas liste noire par nom »). L'agent garde une valeur
// seulement si, une fois l'hôte extrait (dockerapi.EnvHosts), celui-ci est un
// nom connu sur cette machine : le nom d'un conteneur ou d'un service Compose
// de l'inventaire, l'un de ses alias, ou son adresse. Tout le reste est jeté,
// y compris tout mot de passe qui ne s'appelle pas comme un conteneur.
//
// Ce qui part : le nom du conteneur visé, son projet et le port. Jamais le
// nom de la variable ni la valeur : « DB_PASSWORD=db » ne ressort que comme
// « source → db », la fuite étant limitée à un nom déjà public dans
// l'inventaire.

// environmentTargets résout les hôtes d'un conteneur contre l'inventaire. Une
// cible doit partager un réseau avec la source : sans réseau commun, elle ne
// peut pas la joindre. La source elle-même n'est jamais une cible.
func environmentTargets(source container.Observed, hosts dockerapi.EnvHosts, inventory []container.Observed) []container.EnvTarget {
	var targets []container.EnvTarget
	for _, host := range hosts.Hosts() {
		for _, candidate := range inventory {
			if candidate.ContainerID == source.ContainerID || !answersTo(candidate, host.Name) || !sharesNetwork(source, candidate) {
				continue
			}
			target := container.EnvTarget{Name: candidate.Name, Group: candidate.Group, Port: host.Port}
			if !slices.ContainsFunc(targets, func(have container.EnvTarget) bool { return have.Name == target.Name && have.Group == target.Group }) {
				targets = append(targets, target)
			}
		}
	}
	slices.SortFunc(targets, func(a, b container.EnvTarget) int {
		return cmp.Or(strings.Compare(a.Group, b.Group), strings.Compare(a.Name, b.Name))
	})
	if len(targets) > container.MaxEnvTargets {
		targets = targets[:container.MaxEnvTargets]
	}
	return targets
}

// answersTo dit si l'hôte désigne ce conteneur : son nom, son service Compose,
// l'un de ses alias ou l'une de ses adresses. Docker résout sans tenir compte
// de la casse ; les hôtes arrivent déjà en minuscules.
func answersTo(candidate container.Observed, host string) bool {
	if strings.ToLower(candidate.Name) == host || strings.ToLower(candidate.ComposeService) == host {
		return true
	}
	for _, attachment := range candidate.Networks {
		if attachment.IP != "" && attachment.IP == host {
			return true
		}
		for _, alias := range attachment.Aliases {
			if strings.ToLower(alias) == host {
				return true
			}
		}
	}
	return false
}

func sharesNetwork(first, second container.Observed) bool {
	for _, own := range first.Networks {
		for _, other := range second.Networks {
			if own.NetworkID == other.NetworkID {
				return true
			}
		}
	}
	return false
}
