package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/status"
)

// L'administration de la page de statut, dans la coquille : les composants
// avec leurs objets nommés, les incidents avec leur fil, les réglages.
// Tout ce que le public ne voit pas se lit ici.

type componentJSON struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Position  int          `json:"position"`
	Members   []memberJSON `json:"members"`
	Derived   string       `json:"derived"`
	Effective string       `json:"effective"`
	CreatedAt time.Time    `json:"created_at"`
}

// Un objet rattaché : ce qu'il apporte, et s'il apporte quelque chose.
type memberJSON struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	State   string `json:"state"`
	Counts  bool   `json:"counts"`
	Present bool   `json:"present"`
}

type componentsResponse struct {
	// L'adresse publique de la page, à montrer et à copier.
	PublicURL  string          `json:"public_url"`
	Global     string          `json:"global"`
	Components []componentJSON `json:"components"`
}

type componentRequest struct {
	Name     string             `json:"name"`
	Position int                `json:"position"`
	Members  []memberRefRequest `json:"members"`
}

type memberRefRequest struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type incidentJSON struct {
	ID         string             `json:"id"`
	Title      string             `json:"title"`
	Impact     string             `json:"impact"`
	Status     string             `json:"status"`
	StartsAt   *time.Time         `json:"starts_at"`
	EndsAt     *time.Time         `json:"ends_at"`
	Components []componentRefJSON `json:"components"`
	Updates    []updateJSON       `json:"updates"`
	CreatedAt  time.Time          `json:"created_at"`
	UpdatedAt  time.Time          `json:"updated_at"`
	ResolvedAt *time.Time         `json:"resolved_at"`
}

type componentRefJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type incidentsResponse struct {
	Incidents []incidentJSON `json:"incidents"`
}

type incidentRequest struct {
	Title        string     `json:"title"`
	Impact       string     `json:"impact"`
	Status       string     `json:"status"`
	Message      string     `json:"message"`
	ComponentIDs []string   `json:"component_ids"`
	StartsAt     *time.Time `json:"starts_at"`
	EndsAt       *time.Time `json:"ends_at"`
}

type incidentChangeRequest struct {
	Title        string     `json:"title"`
	ComponentIDs []string   `json:"component_ids"`
	StartsAt     *time.Time `json:"starts_at"`
	EndsAt       *time.Time `json:"ends_at"`
}

type updateRequest struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

type pageJSON struct {
	Title        string `json:"title"`
	Announcement string `json:"announcement"`
	Language     string `json:"language"`
	PublicURL    string `json:"public_url"`
}

type pageRequest struct {
	Title        string `json:"title"`
	Announcement string `json:"announcement"`
	Language     string `json:"language"`
}

func componentToJSON(component status.Component) componentJSON {
	members := make([]memberJSON, 0, len(component.Members))
	for _, member := range component.Members {
		members = append(members, memberJSON{
			Kind: string(member.Kind), ID: member.ID, Name: member.Name,
			State: string(member.State), Counts: member.Counts, Present: member.Present,
		})
	}
	return componentJSON{
		ID: component.ID, Name: component.Name, Position: component.Position, Members: members,
		Derived: string(component.Derived), Effective: string(component.Effective), CreatedAt: component.CreatedAt.UTC(),
	}
}

func incidentToJSON(incident status.Incident) incidentJSON {
	components := make([]componentRefJSON, 0, len(incident.Components))
	for _, component := range incident.Components {
		components = append(components, componentRefJSON{ID: component.ID, Name: component.Name})
	}
	return incidentJSON{
		ID: incident.ID, Title: incident.Title, Impact: string(incident.Impact), Status: string(incident.Status),
		StartsAt: timeOrNil(incident.StartsAt), EndsAt: timeOrNil(incident.EndsAt),
		Components: components, Updates: updatesToJSON(incident.Updates),
		CreatedAt: incident.CreatedAt.UTC(), UpdatedAt: incident.UpdatedAt.UTC(), ResolvedAt: timeOrNil(incident.ResolvedAt),
	}
}

func timeOrZero(at *time.Time) time.Time {
	if at == nil {
		return time.Time{}
	}
	return *at
}

func (s *Server) statusPublicURL(r *http.Request) string {
	base, _ := s.resolvePublicURL(r)
	return base + statusPath
}

func (s *Server) listComponents(w http.ResponseWriter, r *http.Request) {
	components, err := s.status.Components(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	response := componentsResponse{PublicURL: s.statusPublicURL(r), Components: make([]componentJSON, 0, len(components))}
	states := []status.State{}
	for _, component := range components {
		if component.Effective != status.StateUnknown {
			states = append(states, component.Effective)
		}
		response.Components = append(response.Components, componentToJSON(component))
	}
	response.Global = string(status.Worst(states))
	s.writeAPI(w, http.StatusOK, response)
}

func (s *Server) getComponent(w http.ResponseWriter, r *http.Request) {
	component, err := s.status.GetComponent(r.Context(), r.PathValue("id"))
	if errors.Is(err, status.ErrNotFound) {
		s.apiNotFound(w)
		return
	}
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusOK, componentToJSON(component))
}

func componentDefinitionOf(request componentRequest) status.ComponentDefinition {
	definition := status.ComponentDefinition{Name: request.Name, Position: request.Position}
	for _, member := range request.Members {
		definition.Members = append(definition.Members, status.MemberRef{Kind: status.MemberKind(member.Kind), ID: member.ID})
	}
	return definition
}

func (s *Server) createComponent(w http.ResponseWriter, r *http.Request) {
	var request componentRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	created, err := s.status.CreateComponent(r.Context(), componentDefinitionOf(request))
	if key, refused := statusRefusalKey(err); refused {
		s.apiRefuse(w, http.StatusUnprocessableEntity, key)
		return
	}
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusCreated, componentToJSON(created))
}

func (s *Server) updateComponent(w http.ResponseWriter, r *http.Request) {
	var request componentRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	updated, err := s.status.UpdateComponent(r.Context(), r.PathValue("id"), componentDefinitionOf(request))
	if errors.Is(err, status.ErrNotFound) {
		s.apiNotFound(w)
		return
	}
	if key, refused := statusRefusalKey(err); refused {
		s.apiRefuse(w, http.StatusUnprocessableEntity, key)
		return
	}
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusOK, componentToJSON(updated))
}

func (s *Server) deleteComponent(w http.ResponseWriter, r *http.Request) {
	s.finishAPIAction(w, r, s.status.DeleteComponent(r.Context(), r.PathValue("id")), status.ErrNotFound)
}

func (s *Server) listIncidents(w http.ResponseWriter, r *http.Request) {
	incidents, err := s.status.Incidents(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	response := incidentsResponse{Incidents: make([]incidentJSON, 0, len(incidents))}
	for _, incident := range incidents {
		response.Incidents = append(response.Incidents, incidentToJSON(incident))
	}
	s.writeAPI(w, http.StatusOK, response)
}

func (s *Server) getIncident(w http.ResponseWriter, r *http.Request) {
	incident, err := s.status.GetIncident(r.Context(), r.PathValue("id"))
	if errors.Is(err, status.ErrNotFound) {
		s.apiNotFound(w)
		return
	}
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusOK, incidentToJSON(incident))
}

func (s *Server) openIncident(w http.ResponseWriter, r *http.Request) {
	var request incidentRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	opened, err := s.status.OpenIncident(r.Context(), status.IncidentDefinition{
		Title:        request.Title,
		Impact:       status.Impact(request.Impact),
		Status:       status.IncidentStatus(request.Status),
		Message:      request.Message,
		ComponentIDs: request.ComponentIDs,
		StartsAt:     timeOrZero(request.StartsAt),
		EndsAt:       timeOrZero(request.EndsAt),
	})
	if key, refused := statusRefusalKey(err); refused {
		s.apiRefuse(w, http.StatusUnprocessableEntity, key)
		return
	}
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusCreated, incidentToJSON(opened))
}

func (s *Server) addIncidentUpdate(w http.ResponseWriter, r *http.Request) {
	var request updateRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	updated, err := s.status.AddUpdate(r.Context(), r.PathValue("id"), status.IncidentStatus(request.Status), request.Message)
	if errors.Is(err, status.ErrNotFound) {
		s.apiNotFound(w)
		return
	}
	if errors.Is(err, status.ErrResolved) {
		s.apiRefuse(w, http.StatusConflict, "incident.already_resolved")
		return
	}
	if key, refused := statusRefusalKey(err); refused {
		s.apiRefuse(w, http.StatusUnprocessableEntity, key)
		return
	}
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusOK, incidentToJSON(updated))
}

func (s *Server) changeIncident(w http.ResponseWriter, r *http.Request) {
	var request incidentChangeRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	changed, err := s.status.ChangeIncident(r.Context(), r.PathValue("id"), status.IncidentChange{
		Title: request.Title, ComponentIDs: request.ComponentIDs,
		StartsAt: timeOrZero(request.StartsAt), EndsAt: timeOrZero(request.EndsAt),
	})
	if errors.Is(err, status.ErrNotFound) {
		s.apiNotFound(w)
		return
	}
	if key, refused := statusRefusalKey(err); refused {
		s.apiRefuse(w, http.StatusUnprocessableEntity, key)
		return
	}
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusOK, incidentToJSON(changed))
}

func (s *Server) deleteIncident(w http.ResponseWriter, r *http.Request) {
	s.finishAPIAction(w, r, s.status.DeleteIncident(r.Context(), r.PathValue("id")), status.ErrNotFound)
}

func (s *Server) getStatusPage(w http.ResponseWriter, r *http.Request) {
	page, err := s.status.Page()
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusOK, pageJSON{Title: page.Title, Announcement: page.Announcement, Language: string(page.Language), PublicURL: s.statusPublicURL(r)})
}

func (s *Server) setStatusPage(w http.ResponseWriter, r *http.Request) {
	var request pageRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	saved, err := s.status.SetPage(r.Context(), status.Page{Title: request.Title, Announcement: request.Announcement, Language: lang.Code(request.Language)})
	if key, refused := statusRefusalKey(err); refused {
		s.apiRefuse(w, http.StatusUnprocessableEntity, key)
		return
	}
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusOK, pageJSON{Title: saved.Title, Announcement: saved.Announcement, Language: string(saved.Language), PublicURL: s.statusPublicURL(r)})
}

func statusRefusalKey(err error) (string, bool) {
	switch {
	case errors.Is(err, status.ErrNameInvalid):
		return "component.name_invalid", true
	case errors.Is(err, status.ErrMemberInvalid):
		return "component.member_invalid", true
	case errors.Is(err, status.ErrTitleInvalid):
		return "incident.title_invalid", true
	case errors.Is(err, status.ErrMessageInvalid):
		return "incident.message_invalid", true
	case errors.Is(err, status.ErrImpactInvalid):
		return "incident.impact_invalid", true
	case errors.Is(err, status.ErrStatusInvalid):
		return "incident.status_invalid", true
	case errors.Is(err, status.ErrComponentInvalid):
		return "incident.component_invalid", true
	case errors.Is(err, status.ErrWindowInvalid):
		return "incident.window_invalid", true
	case errors.Is(err, status.ErrPageTitleInvalid):
		return "statuspage.title_invalid", true
	case errors.Is(err, status.ErrAnnouncementInvalid):
		return "statuspage.announcement_invalid", true
	case errors.Is(err, status.ErrLanguageInvalid):
		return "statuspage.language_invalid", true
	case errors.Is(err, status.ErrTooMany):
		return "component.too_many", true
	}
	return "", false
}
