package deploy

import (
	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// Les refus d'un déploiement. Les premiers disent ce que le service doit
// porter dans l'annuaire, le reste l'état de la machine et du dossier.
var (
	ErrOriginNotCompose = refusal.Invalid("deploy.origin_not_compose", "only a service of compose origin can be deployed")
	ErrProjectMissing   = refusal.Invalid("deploy.project_missing", "the service has no compose project: it names its folder")
	ErrNoUnits          = refusal.Invalid("deploy.no_units", "the service has no unit to deploy")
	ErrUnitNotContainer = refusal.Invalid("deploy.unit_not_container", "a binary unit is not deployed through Docker")
	ErrVersionUnpinned  = refusal.Invalid("deploy.version_unpinned", "every unit must carry a pinned version")
	ErrComposeTooLarge  = refusal.Invalid("deploy.compose_too_large", "the generated compose file is larger than an order carries")
	ErrEnvTooLarge      = refusal.Invalid("deploy.env_too_large", "the .env exceeds 64 KiB")
	ErrEnvInvalid       = refusal.Invalid("deploy.env_invalid", "the .env must be UTF-8 text without a null character")

	ErrRightMissing    = refusal.Conflict("deploy.right_missing", "the agent of this machine may not deploy: re-enroll it with that right")
	ErrAlreadyDeployed = refusal.Conflict("deploy.already_deployed", "the service is already deployed: redeploy it")
	ErrNotDeployed     = refusal.Conflict("deploy.not_deployed", "the service has never been deployed: deploy it first")
	ErrNoAgent         = refusal.Conflict("deploy.no_agent", "the machine of the service has no active agent")
)

// RightState dit si l'agent de la machine porte le droit du prochain geste :
// « unknown » tant qu'il n'a pas rapporté ses droits. « unavailable » : il le
// porte, mais son compte ne peut pas l'exercer (Plan.RightReason dit
// pourquoi). Le serveur l'affiche sans refuser le geste : le rapport date de
// l'ouverture du flux, et l'agent refuse, avec la raison, au moment de l'ordre.
type RightState string

const (
	RightOK          RightState = "ok"
	RightMissing     RightState = "missing"
	RightUnavailable RightState = "unavailable"
	RightUnknown     RightState = "unknown"
)

// Plan est ce que l'opérateur lit avant de déployer : où le service ira, ce
// qu'il y aura, et où il en est.
type Plan struct {
	Service     directory.Service
	MachineName string
	// La racine que l'agent a dite, et le dossier du service dessous.
	RootDir string
	Path    string
	// Vrai quand un déploiement a écrit le dossier sur la machine : le geste
	// est alors « redéployer ».
	Deployed bool
	// Le droit du prochain geste : docker.deploy, puis docker.update. Avec
	// « unavailable », la raison que l'agent a rapportée (agentwire).
	Right       RightState
	RightReason string
	// Le compose.yml que le prochain geste écrira, et les unités dont il
	// monte un dossier de données.
	Compose  string
	DataDirs []string
	// Le dernier ordre de déploiement du service, quel que soit son état ;
	// nil s'il n'y en a jamais eu (ou plus : la purge des runs l'a emporté).
	LastRun *action.Run
}
