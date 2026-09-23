package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/service"
)

// La topologie d'une machine telle que le front la dessine : les services
// avec leurs faits, les groupes qu'un réseau créé par l'opérateur ou par
// Compose forme, les arêtes d'Internet vers un port publié et d'un service
// vers ce dont il dépend. Le front place et trace ; rien ici ne dit où.
type networkResponse struct {
	MachineID   string        `json:"machine_id"`
	MachineName string        `json:"machine_name"`
	Online      bool          `json:"online"`
	Engine      *engineJSON   `json:"engine"`
	Services    []serviceJSON `json:"services"`
	Groups      []groupJSON   `json:"groups"`
	Edges       []edgeJSON    `json:"edges"`
}

type groupJSON struct {
	NetworkID string   `json:"network_id"`
	Name      string   `json:"name"`
	Internal  bool     `json:"internal"`
	Members   []string `json:"members"`
}

type edgeJSON struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Kind     string `json:"kind"`
	Port     int    `json:"port,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	Source   string `json:"source,omitempty"`
}

// La vue d'ensemble lit toutes les machines d'un coup, chacune avec sa
// topologie ; le front en fait un seul dessin, moins détaillé.
type overviewNetworkResponse struct {
	Machines []networkResponse `json:"machines"`
}

func (s *Server) getMachineNetwork(w http.ResponseWriter, r *http.Request) {
	status, err := s.machines.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, machine.ErrNotFound) {
		s.apiNotFound(w)
		return
	}
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	response, err := s.networkOf(r.Context(), status)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusOK, response)
}

func (s *Server) listNetworks(w http.ResponseWriter, r *http.Request) {
	statuses, err := s.machines.List(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	response := overviewNetworkResponse{Machines: make([]networkResponse, 0, len(statuses))}
	for _, status := range statuses {
		network, err := s.networkOf(r.Context(), status)
		if err != nil {
			s.apiInternalError(w, r, err)
			return
		}
		response.Machines = append(response.Machines, network)
	}
	s.writeAPI(w, http.StatusOK, response)
}

func (s *Server) networkOf(ctx context.Context, status machine.Status) (networkResponse, error) {
	topology, err := s.services.Topology(ctx, status.ID)
	if err != nil {
		return networkResponse{}, err
	}
	currents, err := s.currentByService(ctx)
	if err != nil {
		return networkResponse{}, err
	}
	engines, err := s.services.Engines(ctx)
	if err != nil {
		return networkResponse{}, err
	}
	checks, err := s.updates.Checks(ctx, status.ID)
	if err != nil {
		return networkResponse{}, err
	}
	response := networkResponse{
		MachineID: status.ID, MachineName: status.Name, Online: status.Online,
		Services: make([]serviceJSON, 0, len(topology.Services)), Groups: []groupJSON{}, Edges: []edgeJSON{},
	}
	for _, item := range topology.Services {
		response.Services = append(response.Services, serviceToJSON(item, status.Name, currents[item.ID], checks))
	}
	for _, group := range topology.Groups {
		response.Groups = append(response.Groups, groupJSON{NetworkID: group.NetworkID, Name: group.Name, Internal: group.Internal, Members: group.Members})
	}
	for _, edge := range topology.Edges {
		response.Edges = append(response.Edges, edgeToJSON(edge))
	}
	for _, engine := range engines {
		if engine.MachineID == status.ID {
			converted := engineToJSON(engine)
			response.Engine = &converted
		}
	}
	return response, nil
}

func edgeToJSON(edge service.Edge) edgeJSON {
	return edgeJSON{From: edge.From, To: edge.To, Kind: string(edge.Kind), Port: edge.Port, Protocol: edge.Protocol, Source: string(edge.Source)}
}
