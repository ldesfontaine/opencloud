package server

import (
	"context"
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/deploy"
	"github.com/ldesfontaine/opencloud/internal/directory"
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
	// Le droit du prochain geste : ok, missing ou unknown.
	Right    string   `json:"right"`
	Compose  string   `json:"compose"`
	DataDirs []string `json:"data_dirs"`
	// Le dernier ordre de déploiement du service, null s'il n'y en a pas ou
	// plus.
	LastRun *actionRunJSON `json:"last_run"`
}

// Le .env part dans ce corps et nulle part ailleurs : absent ou vide, le
// service est déployé sans .env, et celui d'un déploiement précédent reste
// celui de la machine.
type deployRequest struct {
	Env *string `json:"env"`
}

func deployPlanToJSON(plan deploy.Plan) deployPlanJSON {
	out := deployPlanJSON{
		ServiceID: plan.Service.ID, MachineID: plan.Service.MachineID, MachineName: plan.MachineName,
		RootDir: plan.RootDir, Path: plan.Path, Deployed: plan.Deployed, Right: string(plan.Right),
		Compose: plan.Compose, DataDirs: orEmpty(plan.DataDirs),
	}
	if plan.LastRun != nil {
		converted := actionRunToJSON(*plan.LastRun)
		out.LastRun = &converted
	}
	return out
}

// Ce que le serveur attend du composant deploy : le plan d'un déploiement et
// les deux ordres qui l'exécutent.
type Deployer interface {
	Plan(ctx context.Context, serviceID string) (deploy.Plan, error)
	Deploy(ctx context.Context, serviceID string, env *string) (action.Run, error)
	Redeploy(ctx context.Context, serviceID string, env *string) (action.Run, error)
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
