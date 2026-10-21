package server

import (
	"errors"
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/deletion"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
	"github.com/ldesfontaine/opencloud/internal/status"
)

// Le plan d'une suppression, tel que la boîte de confirmation le montre
// avant le DELETE, et tel que le 409 le rend quand elle est retenue.
type deletionPlanJSON struct {
	Blocked  bool                  `json:"blocked"`
	Blockers []deletionBlockerJSON `json:"blockers"`
	Removes  []deletionCountJSON   `json:"removes"`
	Detaches []deletionCountJSON   `json:"detaches"`
}

type deletionBlockerJSON struct {
	Kind    string               `json:"kind"`
	Objects []deletionObjectJSON `json:"objects"`
}

type deletionObjectJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type deletionCountJSON struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// deletionRefusalJSON est le 409 d'une suppression retenue : la clé du
// refus, et le plan d'aujourd'hui qui dit ce qui la retient.
type deletionRefusalJSON struct {
	Error string           `json:"error"`
	Plan  deletionPlanJSON `json:"plan"`
}

func deletionPlanToJSON(plan deletion.Plan) deletionPlanJSON {
	return deletionPlanJSON{
		Blocked:  plan.IsBlocked(),
		Blockers: httpx.ConvertAll(plan.Blockers, deletionBlockerToJSON),
		Removes:  httpx.ConvertAll(plan.Removes, deletionCountToJSON),
		Detaches: httpx.ConvertAll(plan.Detaches, deletionCountToJSON),
	}
}

func deletionBlockerToJSON(blocker deletion.Blocker) deletionBlockerJSON {
	return deletionBlockerJSON{Kind: string(blocker.Kind), Objects: httpx.ConvertAll(blocker.Objects, deletionObjectToJSON)}
}

func deletionObjectToJSON(object deletion.Object) deletionObjectJSON {
	return deletionObjectJSON{ID: object.ID, Name: object.Name}
}

func deletionCountToJSON(count deletion.Count) deletionCountJSON {
	return deletionCountJSON{Kind: string(count.Kind), Count: count.Count}
}

// writeDeletionPlan répond à la lecture d'un plan : 200 et le plan, ou l'échec.
func (s *Server) writeDeletionPlan(w http.ResponseWriter, r *http.Request, plan deletion.Plan, err, notFound error) {
	if s.failed(w, r, err, notFound) {
		return
	}
	s.writeAPI(w, http.StatusOK, deletionPlanToJSON(plan))
}

func (s *Server) getMachineDeletePlan(w http.ResponseWriter, r *http.Request) {
	plan, err := s.machines.DeletePlan(r.Context(), r.PathValue("id"))
	if errors.Is(err, machine.ErrLocalMachine) {
		s.apiRefuse(w, http.StatusForbidden, "machine.local_kept")
		return
	}
	s.writeDeletionPlan(w, r, plan, err, machine.ErrNotFound)
}

func (s *Server) getDeclaredServiceDeletePlan(w http.ResponseWriter, r *http.Request) {
	plan, err := s.directory.DeleteServicePlan(r.Context(), r.PathValue("id"))
	s.writeDeletionPlan(w, r, plan, err, directory.ErrNotFound)
}

func (s *Server) getProjectDeletePlan(w http.ResponseWriter, r *http.Request) {
	plan, err := s.directory.DeleteProjectPlan(r.Context(), r.PathValue("id"))
	s.writeDeletionPlan(w, r, plan, err, directory.ErrProjectNotFound)
}

func (s *Server) getProbeDeletePlan(w http.ResponseWriter, r *http.Request) {
	plan, err := s.probes.DeletePlan(r.Context(), r.PathValue("id"))
	s.writeDeletionPlan(w, r, plan, err, probe.ErrNotFound)
}

func (s *Server) getJobDeletePlan(w http.ResponseWriter, r *http.Request) {
	plan, err := s.heartbeats.DeletePlan(r.Context(), r.PathValue("id"))
	s.writeDeletionPlan(w, r, plan, err, heartbeat.ErrNotFound)
}

func (s *Server) getComponentDeletePlan(w http.ResponseWriter, r *http.Request) {
	plan, err := s.status.DeleteComponentPlan(r.Context(), r.PathValue("id"))
	s.writeDeletionPlan(w, r, plan, err, status.ErrNotFound)
}

func (s *Server) getPageDeletePlan(w http.ResponseWriter, r *http.Request) {
	plan, err := s.status.DeletePagePlan(r.Context(), r.PathValue("id"))
	s.writeDeletionPlan(w, r, plan, err, status.ErrNotFound)
}
