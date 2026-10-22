package deploy

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path"
	"slices"
	"strings"
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
	sender   Sender
	logger   *slog.Logger
}

func New(services Services, units Units, machines Machines, runs Runs, sender Sender, logger *slog.Logger) *Service {
	return &Service{services: services, units: units, machines: machines, runs: runs, sender: sender, logger: logger}
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
	plan := Plan{
		Service: service, MachineName: target.Name, RootDir: root, Path: path.Join(root, service.ComposeProject),
		Deployed: deployed, MachineOnline: target.Online, Right: rightOf(target, deployed), Compose: string(compose), DataDirs: dataDirs,
	}
	if hasLast {
		plan.LastRun = &last
	}
	return plan, nil
}

// rightOf : « ok » ou « missing » une fois les droits de l'agent rapportés,
// « unknown » avant. Le droit est celui du prochain geste.
func rightOf(target machine.Status, deployed bool) RightState {
	if target.Agent.ReportedAt.IsZero() {
		return RightUnknown
	}
	needed := machine.CapabilityDockerDeploy
	if deployed {
		needed = machine.CapabilityDockerUpdate
	}
	if machine.HasCapability(target.Agent.Capabilities, needed) {
		return RightOK
	}
	return RightMissing
}

// Deploy est le premier geste : le dossier du service n'existe pas encore sur
// la machine. env est le contenu de l'.env à écrire ; nil n'en écrit aucun.
func (s *Service) Deploy(ctx context.Context, serviceID string, env *string) (action.Run, error) {
	return s.send(ctx, serviceID, env, false)
}

// Redeploy réécrit le compose.yml et relance Compose : les versions ont
// changé. env nil laisse l'.env de la machine où il est.
func (s *Service) Redeploy(ctx context.Context, serviceID string, env *string) (action.Run, error) {
	return s.send(ctx, serviceID, env, true)
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
	if !plan.Deployed {
		return action.Run{}, ErrNotDeployed
	}
	if plan.Right == RightMissing {
		return action.Run{}, ErrRightMissing
	}
	if !plan.MachineOnline {
		return action.Run{}, action.ErrMachineOffline
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
	return s.Redeploy(ctx, plan.Service.ID, nil)
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

// send juge, puis envoie. Ordre des contrôles : le plan, le droit, déjà
// déployé ou non, l'.env.
func (s *Service) send(ctx context.Context, serviceID string, env *string, redeploy bool) (action.Run, error) {
	plan, err := s.Plan(ctx, serviceID)
	if err != nil {
		return action.Run{}, err
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
		DataDirs: plan.DataDirs, EnvSent: secret != nil,
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
