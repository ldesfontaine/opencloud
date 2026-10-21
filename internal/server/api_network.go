package server

import (
	"context"
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
)

// La topologie d'une machine telle que le front la dessine : les conteneurs
// avec leurs faits, les groupes qu'un réseau créé par l'opérateur ou par
// Compose forme, les arêtes d'Internet vers un port publié et d'un conteneur
// vers ce dont il dépend. Le front place et trace ; rien ici ne dit où.
type networkResponse struct {
	MachineID   string          `json:"machine_id"`
	MachineName string          `json:"machine_name"`
	Online      bool            `json:"online"`
	Engine      *engineJSON     `json:"engine"`
	Containers  []containerJSON `json:"containers"`
	Groups      []groupJSON     `json:"groups"`
	Edges       []edgeJSON      `json:"edges"`
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
	found, err := s.machines.Get(r.Context(), r.PathValue("id"))
	if s.failed(w, r, err, machine.ErrNotFound) {
		return
	}
	response, err := s.networkOf(r.Context(), found)
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, response)
}

func (s *Server) listNetworks(w http.ResponseWriter, r *http.Request) {
	statuses, err := s.machines.List(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	response := overviewNetworkResponse{Machines: make([]networkResponse, 0, len(statuses))}
	for _, machineStatus := range statuses {
		network, err := s.networkOf(r.Context(), machineStatus)
		if s.failed(w, r, err, nil) {
			return
		}
		response.Machines = append(response.Machines, network)
	}
	s.writeAPI(w, http.StatusOK, response)
}

func (s *Server) networkOf(ctx context.Context, machineStatus machine.Status) (networkResponse, error) {
	topology, err := s.containers.Topology(ctx, machineStatus.ID)
	if err != nil {
		return networkResponse{}, err
	}
	currents, err := s.currentByContainer(ctx)
	if err != nil {
		return networkResponse{}, err
	}
	engines, err := s.reader.ListEngines(ctx)
	if err != nil {
		return networkResponse{}, err
	}
	checks, err := s.updates.Checks(ctx, machineStatus.ID)
	if err != nil {
		return networkResponse{}, err
	}
	response := networkResponse{
		MachineID: machineStatus.ID, MachineName: machineStatus.Name, Online: machineStatus.Online,
		Containers: make([]containerJSON, 0, len(topology.Containers)), Groups: []groupJSON{}, Edges: httpx.ConvertAll(topology.Edges, edgeToJSON),
	}
	for _, item := range topology.Containers {
		response.Containers = append(response.Containers, containerToJSON(item, machineStatus.Name, currents[item.ID], checks))
	}
	for _, group := range topology.Groups {
		response.Groups = append(response.Groups, groupJSON{NetworkID: group.NetworkID, Name: group.Name, Internal: group.Internal, Members: group.Members})
	}
	for _, engine := range engines {
		if engine.MachineID == machineStatus.ID {
			converted := engineToJSON(engine)
			response.Engine = &converted
		}
	}
	return response, nil
}

func edgeToJSON(edge container.Edge) edgeJSON {
	return edgeJSON{From: edge.From, To: edge.To, Kind: string(edge.Kind), Port: edge.Port, Protocol: edge.Protocol, Source: string(edge.Source)}
}
