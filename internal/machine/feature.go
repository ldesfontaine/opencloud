package machine

import "github.com/ldesfontaine/opencloud/internal/release"

// Feature est une fonction que seuls certains agents savent faire : la liste
// fermée de ce que le serveur conditionne à l'agent, et jamais jugée ailleurs
// que par Supports.
type Feature string

const (
	// L'agent revient seul à sa version d'avant quand une mise à jour n'est
	// pas confirmée : sans cela, le serveur ne lui en envoie aucune.
	FeatureAgentRollback Feature = "agent_rollback"
	// L'agent revient seul à la version d'avant d'un service dont la montée
	// n'est pas saine : sans cela, le serveur ne lui en envoie aucune.
	FeatureServiceRollback Feature = "service_rollback"
	// L'agent lit le préfixe de backup.prune. Un agent plus ancien l'ignore et
	// élague « opencloud-… » avec le nombre à garder de n'importe quel plan :
	// le serveur ne lui envoie pas l'élagage d'un plan machine.
	FeaturePrunePrefix Feature = "prune_prefix"
)

// PrunePrefixSinceVersion est la première version d'agent qui a
// FeaturePrunePrefix.
const PrunePrefixSinceVersion = "v0.2.0"

// Supports dit si l'agent sait faire cette fonction. La preuve vient d'où la
// fonction l'a posée : un entier rapporté à l'ouverture du flux (les retours
// arrière), ou la version de l'agent (le préfixe, antérieur aux en-têtes par
// fonction). Une version illisible compte comme trop ancienne.
func (a Agent) Supports(feature Feature) bool {
	switch feature {
	case FeatureAgentRollback:
		return a.RollbackVersion > 0
	case FeatureServiceRollback:
		return a.ServiceRollbackVersion > 0
	case FeaturePrunePrefix:
		return a.versionAtLeast(PrunePrefixSinceVersion)
	}
	return false
}

func (a Agent) versionAtLeast(minimum string) bool {
	wanted, err := release.ParseVersion(minimum)
	if err != nil {
		return false
	}
	version, err := release.ParseVersion(a.Version)
	return err == nil && version.Compare(wanted) >= 0
}
