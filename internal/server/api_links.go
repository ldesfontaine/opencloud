package server

import (
	"context"
	"net/http"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/link"
)

// Les liens d'un service : ceux qui partent de ses instances et ceux qui y
// arrivent. Chaque lien dit d'où il vient (preuve, signal) et depuis quand
// on ne l'a plus revu ; jamais une valeur de configuration.
type serviceLinksResponse struct {
	Links []serviceLinkJSON `json:"links"`
}

type serviceLinkJSON struct {
	ID string `json:"id"`
	// outgoing : le service utilise l'autre ; incoming : l'autre l'utilise.
	Direction   string    `json:"direction"`
	Nature      string    `json:"nature"`
	Proof       string    `json:"proof"`
	Signal      string    `json:"signal"`
	Port        int       `json:"port"`
	FirstSeenAt time.Time `json:"first_seen_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	Stale       bool      `json:"stale"`
	Removable   bool      `json:"removable"`
	// dismissed : une route déduite que vous avez écartée, gardée pour la rétablir.
	Dismissed bool        `json:"dismissed"`
	Self      linkEndJSON `json:"self"`
	Peer      linkEndJSON `json:"peer"`
}

// linkEndJSON : une extrémité. Service et unité sont vides quand aucun
// service déclaré ne porte l'instance ; ContainerID quand un conteneur vivant
// la porte.
type linkEndJSON struct {
	MachineID   string `json:"machine_id"`
	MachineName string `json:"machine_name"`
	Key         string `json:"key"`
	Label       string `json:"label"`
	ServiceID   string `json:"service_id"`
	ServiceName string `json:"service_name"`
	UnitName    string `json:"unit_name"`
	ContainerID string `json:"container_id"`
}

// Le corps d'un lien à écrire : un service de chaque côté, et l'unité quand
// le service en a plusieurs.
type linkRequest struct {
	FromServiceID string `json:"from_service_id"`
	FromUnitID    string `json:"from_unit_id"`
	ToServiceID   string `json:"to_service_id"`
	ToUnitID      string `json:"to_unit_id"`
	Port          int    `json:"port"`
}

func (s *Server) getServiceLinks(w http.ResponseWriter, r *http.Request) {
	found, err := s.directory.ServiceLinks(r.Context(), r.PathValue("id"))
	if s.failed(w, r, err, directory.ErrNotFound) {
		return
	}
	ends, err := s.linkEnds(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	now := s.now()
	response := serviceLinksResponse{Links: make([]serviceLinkJSON, 0, len(found))}
	for _, item := range found {
		direction := "incoming"
		if item.Outgoing {
			direction = "outgoing"
		}
		response.Links = append(response.Links, serviceLinkJSON{
			ID: item.Link.ID, Direction: direction, Nature: string(item.Link.Nature), Proof: string(item.Link.Proof), Signal: string(item.Link.Signal),
			Port: item.Link.Port, FirstSeenAt: item.Link.FirstSeen, LastSeenAt: item.Link.LastSeen,
			Stale: item.Link.Stale(now), Removable: item.Link.Removable(), Dismissed: item.Dismissed,
			Self: ends.describe(item.Self), Peer: ends.describe(item.Peer),
		})
	}
	s.writeAPI(w, http.StatusOK, response)
}

func (s *Server) createLink(w http.ResponseWriter, r *http.Request) {
	var request linkRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	_, err := s.directory.DeclareLink(r.Context(), directory.LinkDefinition{
		FromServiceID: request.FromServiceID, FromUnitID: request.FromUnitID,
		ToServiceID: request.ToServiceID, ToUnitID: request.ToUnitID, Port: request.Port,
	})
	if s.failed(w, r, err, nil) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Les trois gestes sur un lien déduit. Chacun répond 204 ; un lien qui n'est
// plus déduit (adresse publique retirée, déjà décidé) répond 404.
func (s *Server) confirmLink(w http.ResponseWriter, r *http.Request) {
	s.decideLink(w, r, s.directory.ConfirmLink)
}

func (s *Server) dismissLink(w http.ResponseWriter, r *http.Request) {
	s.decideLink(w, r, s.directory.DismissLink)
}

func (s *Server) restoreLink(w http.ResponseWriter, r *http.Request) {
	s.decideLink(w, r, s.directory.RestoreLink)
}

func (s *Server) decideLink(w http.ResponseWriter, r *http.Request, decide func(context.Context, string) error) {
	err := decide(r.Context(), r.PathValue("id"))
	if s.failed(w, r, err, link.ErrNotFound) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteLink(w http.ResponseWriter, r *http.Request) {
	err := s.directory.RemoveLink(r.Context(), r.PathValue("id"))
	if s.failed(w, r, err, link.ErrNotFound) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// linkEnds sait nommer l'extrémité d'un lien : sa machine, et le conteneur
// vivant qui la porte. Le service déclaré, lui, vient de l'annuaire.
type linkEnds struct {
	machineNames map[string]string
	containers   map[link.Endpoint]string
}

func (s *Server) linkEnds(ctx context.Context) (linkEnds, error) {
	statuses, err := s.machines.List(ctx)
	if err != nil {
		return linkEnds{}, err
	}
	living, err := s.containers.List(ctx, "")
	if err != nil {
		return linkEnds{}, err
	}
	ends := linkEnds{machineNames: make(map[string]string, len(statuses)), containers: make(map[link.Endpoint]string, len(living))}
	for _, status := range statuses {
		ends.machineNames[status.ID] = status.Name
	}
	for _, item := range living {
		endpoint := link.Endpoint{MachineID: item.MachineID, Key: container.InstanceKey(item)}
		if _, taken := ends.containers[endpoint]; !taken {
			ends.containers[endpoint] = item.ID
		}
	}
	return ends, nil
}

func (e linkEnds) describe(end directory.LinkEnd) linkEndJSON {
	endpoint := link.Endpoint{MachineID: end.MachineID, Key: end.Key}
	return linkEndJSON{
		MachineID: end.MachineID, MachineName: e.machineNames[end.MachineID], Key: end.Key, Label: link.LabelOf(end.Key),
		ServiceID: end.ServiceID, ServiceName: end.ServiceName, UnitName: end.UnitName, ContainerID: e.containers[endpoint],
	}
}

func (e linkEnds) endpoint(endpoint link.Endpoint) linkEndJSON {
	return e.describe(directory.LinkEnd{MachineID: endpoint.MachineID, Key: endpoint.Key})
}
