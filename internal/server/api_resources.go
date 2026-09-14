package server

import (
	"errors"
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/resource"
	"github.com/ldesfontaine/opencloud/internal/sampler"
)

const (
	defaultWindow     = "1h"
	codeWindowUnknown = "window_unknown"
)

// Les ressources d'une machine telles que le front les lit : la dernière
// lecture, et si elle est assez fraîche pour valoir « maintenant ». Sans
// lecture, ou trop vieille, available est faux ; jamais des zéros. Les
// pourcentages se calculent dans le navigateur.
type currentJSON struct {
	MachineID string           `json:"machine_id"`
	Available bool             `json:"available"`
	Sample    *sampler.Reading `json:"sample"`
}

type resourcesResponse struct {
	Machines []currentJSON `json:"machines"`
}

// Un historique : la fenêtre, son pas, et les points datés du début de
// leur seau, en UTC.
type historyResponse struct {
	MachineID   string            `json:"machine_id"`
	Window      string            `json:"window"`
	SpanSeconds int               `json:"span_seconds"`
	StepSeconds int               `json:"step_seconds"`
	Points      []sampler.Reading `json:"points"`
}

func currentToJSON(current resource.Current) currentJSON {
	response := currentJSON{MachineID: current.MachineID, Available: current.Available}
	if current.Sample != nil {
		reading := current.Sample.Reading
		reading.SampledAt = reading.SampledAt.UTC()
		response.Sample = &reading
	}
	return response
}

// Les machines qui ont déjà mesuré ; le front dit « indisponible » pour
// celles qui manquent.
func (s *Server) listResources(w http.ResponseWriter, r *http.Request) {
	currents, err := s.resources.CurrentAll(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	response := resourcesResponse{Machines: make([]currentJSON, 0, len(currents))}
	for _, current := range currents {
		response.Machines = append(response.Machines, currentToJSON(current))
	}
	s.writeAPI(w, http.StatusOK, response)
}

func (s *Server) getMachineResources(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.machineExists(w, r, id) {
		return
	}
	current, err := s.resources.Current(r.Context(), id)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusOK, currentToJSON(current))
}

func (s *Server) getMachineHistory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.machineExists(w, r, id) {
		return
	}
	name := r.URL.Query().Get("window")
	if name == "" {
		name = defaultWindow
	}
	window, points, err := s.resources.History(r.Context(), id, name)
	if errors.Is(err, resource.ErrWindowUnknown) {
		s.writeAPIError(w, http.StatusBadRequest, codeWindowUnknown)
		return
	}
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusOK, historyResponse{
		MachineID:   id,
		Window:      window.Name,
		SpanSeconds: int(window.Span.Seconds()),
		StepSeconds: int(window.Step.Seconds()),
		Points:      points,
	})
}

// machineExists répond 404 ou 500 et rend faux quand le handler doit
// s'arrêter là.
func (s *Server) machineExists(w http.ResponseWriter, r *http.Request, id string) bool {
	_, err := s.machines.Get(r.Context(), id)
	if errors.Is(err, machine.ErrNotFound) {
		s.apiNotFound(w)
		return false
	}
	if err != nil {
		s.apiInternalError(w, r, err)
		return false
	}
	return true
}
