package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/deploy"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
)

// Le .env tient jusqu'à 64 KiB de texte ; écrit en JSON (retours à la ligne,
// guillemets) il peut presque doubler. Le corps d'un déploiement a donc sa
// propre borne, plus large que celle d'un formulaire.
const maxDeployBody = 256 << 10

// Ce que l'opérateur lit avant de déployer ; voir deploy.Plan.
type deployPlanJSON struct {
	ServiceID   string `json:"service_id"`
	MachineID   string `json:"machine_id"`
	MachineName string `json:"machine_name"`
	// La racine que l'agent a dite (vide tant qu'il ne l'a pas dite), et le
	// dossier du service dessous.
	RootDir string `json:"root_dir"`
	Path    string `json:"path"`
	// Vrai quand un déploiement a écrit le dossier : le geste est alors
	// « redéployer ».
	Deployed bool `json:"deployed"`
	// Le droit du prochain geste : ok, missing, unavailable ou unknown. Avec
	// unavailable, right_reason dit pourquoi le compte de l'agent ne peut pas
	// l'exercer ; le geste reste permis, l'agent refusera avec la raison.
	Right       string   `json:"right"`
	RightReason string   `json:"right_reason,omitempty"`
	Compose     string   `json:"compose"`
	DataDirs    []string `json:"data_dirs"`
	// Les noms des autres services déclarés qui partagent le projet Compose :
	// tant qu'il y en a, déployer est refusé (deploy.project_shared).
	SharedWith []string `json:"shared_with"`
	// Le dernier ordre de déploiement du service, null s'il n'y en a pas ou
	// plus.
	LastRun *actionRunJSON `json:"last_run"`
	// Vrai quand l'agent sait revenir seul à la version d'avant ; faux, la
	// montée de version est refusée (deploy.rollback_unavailable).
	RollbackCapable bool `json:"rollback_capable"`
	// La dernière montée de version du service, null s'il n'y en a pas.
	LastUpdate *serviceUpdateJSON `json:"last_update"`
}

// Une montée de version : de quelle version à quelle autre, et ce qu'elle est
// devenue (pending, confirmed, rolled_back, failed ou refused).
type serviceUpdateJSON struct {
	UnitName    string     `json:"unit_name"`
	FromVersion string     `json:"from_version"`
	ToVersion   string     `json:"to_version"`
	State       string     `json:"state"`
	RequestedAt time.Time  `json:"requested_at"`
	SettledAt   *time.Time `json:"settled_at"`
}

// Le .env part dans ce corps et nulle part ailleurs : absent ou vide, le
// service est déployé sans .env, et celui d'un déploiement précédent reste
// celui de la machine.
type deployRequest struct {
	Env *string `json:"env"`
}

// Passer une unité à la version que l'agent propose : le tag, rien d'autre.
type unitUpgradeRequest struct {
	Version string `json:"version"`
}

func deployPlanToJSON(plan deploy.Plan) deployPlanJSON {
	out := deployPlanJSON{
		ServiceID: plan.Service.ID, MachineID: plan.Service.MachineID, MachineName: plan.MachineName,
		RootDir: plan.RootDir, Path: plan.Path, Deployed: plan.Deployed, Right: string(plan.Right),
		RightReason: plan.RightReason, Compose: plan.Compose, DataDirs: orEmpty(plan.DataDirs), SharedWith: orEmpty(plan.SharedWith),
	}
	out.RollbackCapable = plan.CanRollBack
	if plan.LastRun != nil {
		converted := actionRunToJSON(*plan.LastRun)
		out.LastRun = &converted
	}
	if plan.LastUpdate != nil {
		out.LastUpdate = &serviceUpdateJSON{
			UnitName: plan.LastUpdate.UnitName, FromVersion: plan.LastUpdate.FromVersion, ToVersion: plan.LastUpdate.ToVersion,
			State: string(plan.LastUpdate.State), RequestedAt: plan.LastUpdate.RequestedAt.UTC(), SettledAt: httpx.TimeOrNil(plan.LastUpdate.SettledAt),
		}
	}
	return out
}

// Ce que le serveur attend du composant deploy : le plan d'un déploiement et
// les deux ordres qui l'exécutent.
type Deployer interface {
	Plan(ctx context.Context, serviceID string) (deploy.Plan, error)
	Deploy(ctx context.Context, serviceID string, env *string) (action.Run, error)
	Redeploy(ctx context.Context, serviceID string, env *string) (action.Run, error)
	Upgrade(ctx context.Context, unitID, version string) (action.Run, error)
}

// getDeployPlan montre ce que le prochain déploiement ferait, sans rien
// envoyer.
func (s *Server) getDeployPlan(w http.ResponseWriter, r *http.Request) {
	plan, err := s.deploy.Plan(r.Context(), r.PathValue("id"))
	if s.failed(w, r, err, directory.ErrNotFound) {
		return
	}
	s.writeAPI(w, http.StatusOK, deployPlanToJSON(plan))
}

// deployService envoie docker.deploy ; redeployService, docker.update. La
// réponse est l'ordre, dont l'état avance ensuite par le direct.
func (s *Server) deployService(w http.ResponseWriter, r *http.Request) {
	s.sendDeployment(w, r, s.deploy.Deploy)
}

func (s *Server) redeployService(w http.ResponseWriter, r *http.Request) {
	s.sendDeployment(w, r, s.deploy.Redeploy)
}

// upgradeUnit change la version déclarée d'une unité et redéploie son service
// en un geste. Comme un redéploiement, la réponse est l'ordre.
func (s *Server) upgradeUnit(w http.ResponseWriter, r *http.Request) {
	var request unitUpgradeRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	run, err := s.deploy.Upgrade(r.Context(), r.PathValue("id"), request.Version)
	if errors.Is(err, directory.ErrUnitNotFound) {
		s.apiRefuse(w, http.StatusNotFound, "directory.unit_not_found")
		return
	}
	if s.failed(w, r, err, directory.ErrNotFound) {
		return
	}
	s.writeAPI(w, http.StatusCreated, actionRunToJSON(run))
}

func (s *Server) sendDeployment(w http.ResponseWriter, r *http.Request, send func(context.Context, string, *string) (action.Run, error)) {
	var request deployRequest
	if !readBounded(w, r, &request, maxDeployBody) {
		s.writeAPIError(w, http.StatusBadRequest, codeBadJSON)
		return
	}
	run, err := send(r.Context(), r.PathValue("id"), request.Env)
	if s.failed(w, r, err, directory.ErrNotFound) {
		return
	}
	s.writeAPI(w, http.StatusCreated, actionRunToJSON(run))
}
