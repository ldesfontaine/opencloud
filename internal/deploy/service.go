package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// Services est ce que le composant lit de l'annuaire ; directory.ErrNotFound
// pour un service qui n'existe pas.
type Services interface {
	GetDeclaredService(ctx context.Context, id string) (directory.Service, error)
	// ListDeclaredServices : les services d'une machine, pour savoir si un
	// autre partage le projet Compose du dossier.
	ListDeclaredServices(ctx context.Context, filter directory.Filter) ([]directory.Service, error)
	// GetUnit rend une unité, directory.ErrUnitNotFound si elle n'existe plus.
	GetUnit(ctx context.Context, id string) (directory.Unit, error)
}

// Units est ce que le composant demande à l'annuaire pour changer la version
// d'une unité : la version que l'agent propose, et l'écriture, qui passe par
// le composant de l'annuaire (rapprochement, audit, direct).
type Units interface {
	OfferedVersion(ctx context.Context, unitID string) (string, bool, error)
	SetUnitVersion(ctx context.Context, unitID, version string) error
}

// Machines est ce que le composant lit des machines : leur agent, ses droits
// et sa racine.
type Machines interface {
	Get(ctx context.Context, id string) (machine.Status, error)
}

// Runs est ce que le composant lit des ordres déjà envoyés pour un service.
type Runs interface {
	// LastServiceRun rend le dernier ordre de ces genres, quel que soit son
	// état.
	LastServiceRun(ctx context.Context, serviceID string, kinds []agentwire.Kind) (action.Run, bool, error)
	// LastFolderRun rend le dernier qui a dit quelque chose du dossier du
	// service sur la machine (le réussi, le Compose en échec, le coupé, le
	// refusé faute de dossier).
	LastFolderRun(ctx context.Context, serviceID string, kinds []agentwire.Kind) (action.Run, bool, error)
}

// Sender envoie un ordre signé à l'agent d'une machine : le composant action.
type Sender interface {
	SendForService(ctx context.Context, order action.ServiceOrder) (action.Run, error)
}

// Service déploie. L'audit est celui de l'ordre (action.order) : il porte le
// service, le genre, et « env: sent » quand un .env part.
type Service struct {
	services Services
	units    Units
	machines Machines
	runs     Runs
	updates  UpdateStore
	sender   Sender
	logger   *slog.Logger
	now      func() time.Time
}

func New(services Services, units Units, machines Machines, runs Runs, updates UpdateStore, sender Sender, logger *slog.Logger) *Service {
	return &Service{services: services, units: units, machines: machines, runs: runs, updates: updates, sender: sender, logger: logger, now: time.Now}
}

func deploymentKinds() []agentwire.Kind {
	return []agentwire.Kind{agentwire.KindDockerDeploy, agentwire.KindDockerUpdate}
}

// Plan dit ce que déployer ce service ferait. Il refuse ce qui ne peut pas
// l'être (le service, sa machine), mais pas un droit qui manque : le plan le
// montre. Ordre des contrôles : le service, son origine et ses unités, sa
// machine et son agent.
func (s *Service) Plan(ctx context.Context, serviceID string) (Plan, error) {
	service, err := s.services.GetDeclaredService(ctx, serviceID)
	if err != nil {
		return Plan{}, err
	}
	compose, dataDirs, err := Render(service)
	if err != nil {
		return Plan{}, err
	}
	sharedWith, err := s.servicesSharingProject(ctx, service)
	if err != nil {
		return Plan{}, err
	}
	target, err := s.machines.Get(ctx, service.MachineID)
	if err != nil {
		return Plan{}, fmt.Errorf("get machine: %w", err)
	}
	if !target.Agent.IsActive() {
		return Plan{}, ErrNoAgent
	}
	folder, found, err := s.runs.LastFolderRun(ctx, serviceID, deploymentKinds())
	if err != nil {
		return Plan{}, fmt.Errorf("read deployments: %w", err)
	}
	deployed := found && folderExists(folder)
	last, hasLast, err := s.runs.LastServiceRun(ctx, serviceID, deploymentKinds())
	if err != nil {
		return Plan{}, fmt.Errorf("read last deployment: %w", err)
	}
	root := target.Agent.RootDir
	if root == "" {
		root = brand.WorkspaceRoot
	}
	right, reason := rightOf(target, deployed)
	plan := Plan{
		Service: service, MachineName: target.Name, RootDir: root, Path: path.Join(root, service.ComposeProject),
		Deployed: deployed, MachineOnline: target.Online, Right: right, RightReason: reason, Compose: string(compose), DataDirs: dataDirs,
		SharedWith: sharedWith, CanRollBack: target.Agent.CanRollBackService(),
	}
	if hasLast {
		plan.LastRun = &last
	}
	if s.updates != nil {
		update, found, err := s.updates.LatestServiceUpdate(ctx, serviceID)
		if err != nil {
			return Plan{}, fmt.Errorf("read last version change: %w", err)
		}
		if found {
			plan.LastUpdate = &update
			// Une montée dont l'agent n'a rien rapporté en une heure n'est plus
			// jugée : l'écran ne la dit pas « en cours » pour toujours.
			if update.State == UpdatePending && update.RequestedAt.Before(s.now().Add(-updateInFlightWindow)) {
				plan.LastUpdate.State = UpdateFailed
				plan.LastUpdate.Reason = "timeout"
			}
		}
	}
	return plan, nil
}

// servicesSharingProject rend les noms des autres services déclarés de la
// machine qui ont le même projet Compose : le fichier écrit ne décrit que les
// unités de ce service, il retirerait celles des autres. Le plan le dit ;
// c'est l'écriture (send, Upgrade) qui refuse.
func (s *Service) servicesSharingProject(ctx context.Context, service directory.Service) ([]string, error) {
	onMachine, err := s.services.ListDeclaredServices(ctx, directory.Filter{MachineID: service.MachineID})
	if err != nil {
		return nil, fmt.Errorf("list machine services: %w", err)
	}
	var names []string
	for _, other := range onMachine {
		if other.ID != service.ID && other.Origin == directory.OriginCompose && other.ComposeProject == service.ComposeProject {
			names = append(names, other.Name)
		}
	}
	return names, nil
}

// rightOf : « ok » ou « missing » une fois les droits de l'agent rapportés,
// « unknown » avant. Le droit est celui du prochain geste. « unavailable » et
// sa raison quand l'agent le porte sans pouvoir l'exercer.
func rightOf(target machine.Status, deployed bool) (RightState, string) {
	if target.Agent.ReportedAt.IsZero() {
		return RightUnknown, ""
	}
	needed := machine.CapabilityDockerDeploy
	if deployed {
		needed = machine.CapabilityDockerUpdate
	}
	if !machine.HasCapability(target.Agent.Capabilities, needed) {
		return RightMissing, ""
	}
	if reason := target.Agent.UnavailableReason(needed); reason != "" {
		return RightUnavailable, reason
	}
	return RightOK, ""
}

// Deploy est le premier geste : le dossier du service n'existe pas encore sur
// la machine. env est le contenu de l'.env à écrire ; nil n'en écrit aucun.
func (s *Service) Deploy(ctx context.Context, serviceID string, env *string) (action.Run, error) {
	return s.send(ctx, serviceID, env, false, nil)
}

// Redeploy réécrit le compose.yml et relance Compose : les versions ont
// changé. env nil laisse l'.env de la machine où il est.
func (s *Service) Redeploy(ctx context.Context, serviceID string, env *string) (action.Run, error) {
	return s.send(ctx, serviceID, env, true, nil)
}

// Upgrade passe une unité d'un service déployé à la version que l'agent
// propose, en un geste : la version déclarée change dans l'annuaire, puis le
// service est redéployé (l'.env de la machine reste). Tout se juge avant la
// première écriture : un refus ne change rien. Ce qui reste après l'écriture
// (la machine qui tombe entre les deux, l'envoi qui échoue) laisse la
// version changée : l'erreur revient à l'opérateur, qui redéploie, comme avec
// les deux gestes séparés.
func (s *Service) Upgrade(ctx context.Context, unitID, version string) (action.Run, error) {
	unit, err := s.services.GetUnit(ctx, unitID)
	if err != nil {
		return action.Run{}, err
	}
	if unit.Kind != directory.UnitContainer {
		return action.Run{}, directory.ErrUnitNotContainer
	}
	plan, err := s.Plan(ctx, unit.ServiceID)
	if err != nil {
		return action.Run{}, err
	}
	if len(plan.SharedWith) > 0 {
		return action.Run{}, ErrProjectShared
	}
	if !plan.Deployed {
		return action.Run{}, ErrNotDeployed
	}
	if plan.Right == RightMissing {
		return action.Run{}, ErrRightMissing
	}
	if !plan.MachineOnline {
		return action.Run{}, action.ErrMachineOffline
	}
	if !plan.CanRollBack {
		return action.Run{}, ErrRollbackUnavailable
	}
	if err := s.refuseWhileJudging(ctx, plan.Service.MachineID); err != nil {
		return action.Run{}, err
	}
	if unit.Digest != "" {
		return action.Run{}, ErrUnitPinnedDigest
	}
	offered, isOffered, err := s.units.OfferedVersion(ctx, unitID)
	if err != nil {
		return action.Run{}, err
	}
	// Égalité stricte : pas de version « proche » de celle qui est offerte.
	if !isOffered || version != offered {
		return action.Run{}, ErrVersionNotOffered
	}
	// La forme d'un tag, jugée ici avant le rendu : le motif du compose admet
	// « : » et « / » dans le nom de l'image, pas l'annuaire dans une version.
	if err := directory.CheckVersion(version); err != nil {
		return action.Run{}, err
	}
	// Le compose de la nouvelle version se rend d'abord : un tag que le rendu
	// refuse ne doit pas avoir été écrit.
	if _, _, err := Render(withVersion(plan.Service, unitID, version)); err != nil {
		return action.Run{}, err
	}
	if err := s.units.SetUnitVersion(ctx, unitID, version); err != nil {
		return action.Run{}, err
	}
	s.logger.Info("unit upgrade", "service_id", plan.Service.ID, "unit_id", unitID, "from", unit.Version, "to", version)
	safety := agentwire.DefaultServiceRollback()
	run, err := s.send(ctx, plan.Service.ID, nil, true, &safety)
	if err != nil {
		return action.Run{}, err
	}
	s.recordUpdate(ctx, run, unit, unit.Version, version)
	return run, nil
}

// withVersion rend le service avec la version d'une de ses unités changée ;
// l'original n'est pas touché.
func withVersion(service directory.Service, unitID, version string) directory.Service {
	service.Units = slices.Clone(service.Units)
	for index := range service.Units {
		if service.Units[index].ID == unitID {
			service.Units[index].Version = version
		}
	}
	return service
}

// send juge, puis envoie. safety pose le filet d'une montée de version ; nil :
// un redéploiement sans filet.
// Ordre des contrôles : le plan, le projet partagé, le droit, déjà
// déployé ou non, l'.env.
func (s *Service) send(ctx context.Context, serviceID string, env *string, redeploy bool, safety *agentwire.ServiceRollback) (action.Run, error) {
	plan, err := s.Plan(ctx, serviceID)
	if err != nil {
		return action.Run{}, err
	}
	if len(plan.SharedWith) > 0 {
		return action.Run{}, ErrProjectShared
	}
	if plan.Right == RightMissing {
		return action.Run{}, ErrRightMissing
	}
	if redeploy && !plan.Deployed {
		return action.Run{}, ErrNotDeployed
	}
	if !redeploy && plan.Deployed {
		return action.Run{}, ErrAlreadyDeployed
	}
	secret, err := secretOf(env)
	if err != nil {
		return action.Run{}, err
	}
	params, err := json.Marshal(agentwire.DockerParams{
		ServiceID: serviceID, Project: plan.Service.ComposeProject, Compose: plan.Compose,
		DataDirs: plan.DataDirs, EnvSent: secret != nil, Rollback: safety,
	})
	if err != nil {
		return action.Run{}, fmt.Errorf("encode params: %w", err)
	}
	order := action.ServiceOrder{
		MachineID: plan.Service.MachineID, ServiceID: serviceID, Kind: agentwire.KindDockerDeploy, Params: params,
	}
	if redeploy {
		order.Kind = agentwire.KindDockerUpdate
	}
	if secret != nil {
		order.Secret, order.SecretLabel = secret, "env"
	}
	run, err := s.sender.SendForService(ctx, order)
	if err != nil {
		return action.Run{}, err
	}
	s.logger.Info("deployment sent", "service_id", serviceID, "run_id", run.ID, "kind", string(run.Kind), "env_sent", secret != nil)
	return run, nil
}

// secretOf prépare le secret de l'ordre : le contenu de l'.env, jugé. nil
// quand il n'y en a pas (absent ou vide). La limite est celle du secret signé
// avec l'ordre, une fois écrit en JSON.
func secretOf(env *string) (json.RawMessage, error) {
	if env == nil || *env == "" {
		return nil, nil
	}
	if !utf8.ValidString(*env) || strings.ContainsRune(*env, 0) {
		return nil, ErrEnvInvalid
	}
	secret, err := json.Marshal(agentwire.DockerSecret{Env: *env})
	if err != nil {
		return nil, fmt.Errorf("encode secret: %w", err)
	}
	if len(secret) > agentwire.MaxSecretBytes {
		return nil, ErrEnvTooLarge
	}
	return secret, nil
}

// folderExists dit ce que le dernier constat de l'agent dit du dossier : seul
// un refus faute de dossier dit qu'il n'existe pas. Un coupé (timeout) a pu
// l'écrire, et l'agent ne retire pas un dossier qu'il n'a pas fini.
func folderExists(run action.Run) bool {
	return !(run.State == action.StateRefused && run.Reason == agentwire.ReasonServiceDirMissing)
}

func jsonOf(value any) (json.RawMessage, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode params: %w", err)
	}
	return encoded, nil
}
