package server

import (
	"net/http"
	"time"

	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
	"github.com/ldesfontaine/opencloud/internal/status"
)

// L'administration des pages de statut, dans la coquille : les pages, leurs
// composants avec leurs objets nommés, les incidents avec leur fil. Tout ce
// que le public ne voit pas se lit ici.

type componentJSON struct {
	ID        string       `json:"id"`
	PageID    string       `json:"page_id"`
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

// Les composants d'une page (?page=<id>), ou de toutes sans le paramètre :
// l'adresse publique est alors vide, l'état global celui de tous.
type componentsResponse struct {
	// L'adresse publique de la page, à montrer et à copier.
	PublicURL  string          `json:"public_url"`
	Global     string          `json:"global"`
	Components []componentJSON `json:"components"`
}

// page_id ne compte qu'à la création : un composant ne change pas de page.
type componentRequest struct {
	PageID   string             `json:"page_id"`
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
	Targets    []targetJSON       `json:"targets"`
	Components []componentRefJSON `json:"components"`
	Updates    []updateJSON       `json:"updates"`
	CreatedAt  time.Time          `json:"created_at"`
	UpdatedAt  time.Time          `json:"updated_at"`
	ResolvedAt *time.Time         `json:"resolved_at"`
}

// Une mise à jour du fil d'un incident, telle que l'opérateur la lit.
type updateJSON struct {
	Status    string    `json:"status"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

// Un objet de l'annuaire que l'incident vise.
type targetJSON struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Un composant que les cibles de l'incident touchent, toutes pages confondues.
type componentRefJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type incidentsResponse struct {
	Incidents []incidentJSON `json:"incidents"`
}

type incidentRequest struct {
	Title    string             `json:"title"`
	Impact   string             `json:"impact"`
	Status   string             `json:"status"`
	Message  string             `json:"message"`
	Targets  []memberRefRequest `json:"targets"`
	StartsAt *time.Time         `json:"starts_at"`
	EndsAt   *time.Time         `json:"ends_at"`
}

type incidentChangeRequest struct {
	Title    string             `json:"title"`
	Targets  []memberRefRequest `json:"targets"`
	StartsAt *time.Time         `json:"starts_at"`
	EndsAt   *time.Time         `json:"ends_at"`
}

type updateRequest struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

// Une page de statut ; project_id vide : la page n'est rangée dans aucun
// projet. La langue est résolue : vide en base, c'est celle de l'interface.
type pageJSON struct {
	ID           string    `json:"id"`
	ProjectID    string    `json:"project_id"`
	Slug         string    `json:"slug"`
	Title        string    `json:"title"`
	Announcement string    `json:"announcement"`
	Language     string    `json:"language"`
	PublicURL    string    `json:"public_url"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type pagesResponse struct {
	Pages []pageJSON `json:"pages"`
}

type pageRequest struct {
	Slug         string `json:"slug"`
	Title        string `json:"title"`
	Announcement string `json:"announcement"`
	Language     string `json:"language"`
	ProjectID    string `json:"project_id"`
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
		ID: component.ID, PageID: component.PageID, Name: component.Name, Position: component.Position, Members: members,
		Derived: string(component.Derived), Effective: string(component.Effective), CreatedAt: component.CreatedAt.UTC(),
	}
}

func updatesToJSON(updates []status.Update) []updateJSON {
	return httpx.ConvertAll(updates, func(update status.Update) updateJSON {
		return updateJSON{Status: string(update.Status), Message: update.Message, CreatedAt: update.CreatedAt.UTC()}
	})
}

func incidentToJSON(incident status.Incident) incidentJSON {
	targets := httpx.ConvertAll(incident.Targets, func(target status.Target) targetJSON {
		return targetJSON{Kind: string(target.Kind), ID: target.ID, Name: target.Name}
	})
	components := httpx.ConvertAll(incident.Components, func(component status.ComponentRef) componentRefJSON {
		return componentRefJSON{ID: component.ID, Name: component.Name}
	})
	return incidentJSON{
		ID: incident.ID, Title: incident.Title, Impact: string(incident.Impact), Status: string(incident.Status),
		StartsAt: httpx.TimeOrNil(incident.StartsAt), EndsAt: httpx.TimeOrNil(incident.EndsAt),
		Targets: targets, Components: components, Updates: updatesToJSON(incident.Updates),
		CreatedAt: incident.CreatedAt.UTC(), UpdatedAt: incident.UpdatedAt.UTC(), ResolvedAt: httpx.TimeOrNil(incident.ResolvedAt),
	}
}

func timeOrZero(at *time.Time) time.Time {
	if at == nil {
		return time.Time{}
	}
	return *at
}

// statusPublicURL rend l'adresse publique d'une page : /statut/<slug>.
func (s *Server) statusPublicURL(r *http.Request, slug string) string {
	base, _ := s.resolvePublicURL(r)
	return base + statusPath + "/" + slug
}

func (s *Server) pageToJSON(r *http.Request, page status.Page) pageJSON {
	return pageJSON{
		ID: page.ID, ProjectID: page.ProjectID, Slug: page.Slug, Title: page.Title,
		Announcement: page.Announcement, Language: string(page.Language), PublicURL: s.statusPublicURL(r, page.Slug),
		CreatedAt: page.CreatedAt.UTC(), UpdatedAt: page.UpdatedAt.UTC(),
	}
}

func pageDefinitionOf(request pageRequest) status.PageDefinition {
	return status.PageDefinition{
		Slug: request.Slug, Title: request.Title, Announcement: request.Announcement,
		Language: lang.Code(request.Language), ProjectID: request.ProjectID,
	}
}

func (s *Server) listPages(w http.ResponseWriter, r *http.Request) {
	pages, err := s.status.ListPages(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	response := pagesResponse{Pages: make([]pageJSON, 0, len(pages))}
	for _, page := range pages {
		response.Pages = append(response.Pages, s.pageToJSON(r, page))
	}
	s.writeAPI(w, http.StatusOK, response)
}

func (s *Server) getPage(w http.ResponseWriter, r *http.Request) {
	page, err := s.status.GetPage(r.Context(), r.PathValue("id"))
	if s.failed(w, r, err, status.ErrNotFound) {
		return
	}
	s.writeAPI(w, http.StatusOK, s.pageToJSON(r, page))
}

func (s *Server) createPage(w http.ResponseWriter, r *http.Request) {
	var request pageRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	created, err := s.status.CreatePage(r.Context(), pageDefinitionOf(request))
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusCreated, s.pageToJSON(r, created))
}

func (s *Server) updatePage(w http.ResponseWriter, r *http.Request) {
	var request pageRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	updated, err := s.status.UpdatePage(r.Context(), r.PathValue("id"), pageDefinitionOf(request))
	if s.failed(w, r, err, status.ErrNotFound) {
		return
	}
	s.writeAPI(w, http.StatusOK, s.pageToJSON(r, updated))
}

func (s *Server) deletePage(w http.ResponseWriter, r *http.Request) {
	s.finishAPIAction(w, r, s.status.DeletePage(r.Context(), r.PathValue("id")), status.ErrNotFound)
}

// listComponents rend les composants d'une page, ?page=<id>, avec son
// adresse publique et son état global ; sans le paramètre, tous.
func (s *Server) listComponents(w http.ResponseWriter, r *http.Request) {
	pageID := r.URL.Query().Get("page")
	response := componentsResponse{}
	if pageID != "" {
		page, err := s.status.GetPage(r.Context(), pageID)
		if s.failed(w, r, err, status.ErrNotFound) {
			return
		}
		response.PublicURL = s.statusPublicURL(r, page.Slug)
	}
	components, err := s.status.Components(r.Context(), pageID)
	if s.failed(w, r, err, nil) {
		return
	}
	response.Components = make([]componentJSON, 0, len(components))
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
	if s.failed(w, r, err, status.ErrNotFound) {
		return
	}
	s.writeAPI(w, http.StatusOK, componentToJSON(component))
}

func componentDefinitionOf(request componentRequest) status.ComponentDefinition {
	return status.ComponentDefinition{PageID: request.PageID, Name: request.Name, Position: request.Position, Members: memberRefsOf(request.Members)}
}

// memberRefsOf rend nil pour une liste vide, comme la définition l'attend.
func memberRefsOf(requests []memberRefRequest) []status.MemberRef {
	var refs []status.MemberRef
	for _, request := range requests {
		refs = append(refs, status.MemberRef{Kind: status.MemberKind(request.Kind), ID: request.ID})
	}
	return refs
}

func (s *Server) createComponent(w http.ResponseWriter, r *http.Request) {
	var request componentRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	created, err := s.status.CreateComponent(r.Context(), componentDefinitionOf(request))
	if s.failed(w, r, err, nil) {
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
	if s.failed(w, r, err, status.ErrNotFound) {
		return
	}
	s.writeAPI(w, http.StatusOK, componentToJSON(updated))
}

func (s *Server) deleteComponent(w http.ResponseWriter, r *http.Request) {
	s.finishAPIAction(w, r, s.status.DeleteComponent(r.Context(), r.PathValue("id")), status.ErrNotFound)
}

// listIncidents rend tous les incidents, ou ceux qui touchent un composant
// de la page ?page=<id>.
func (s *Server) listIncidents(w http.ResponseWriter, r *http.Request) {
	incidents, err := s.status.Incidents(r.Context(), r.URL.Query().Get("page"))
	if s.failed(w, r, err, status.ErrNotFound) {
		return
	}
	response := incidentsResponse{Incidents: httpx.ConvertAll(incidents, incidentToJSON)}
	s.writeAPI(w, http.StatusOK, response)
}

func (s *Server) getIncident(w http.ResponseWriter, r *http.Request) {
	incident, err := s.status.GetIncident(r.Context(), r.PathValue("id"))
	if s.failed(w, r, err, status.ErrNotFound) {
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
		Title:    request.Title,
		Impact:   status.Impact(request.Impact),
		Status:   status.IncidentStatus(request.Status),
		Message:  request.Message,
		Targets:  memberRefsOf(request.Targets),
		StartsAt: timeOrZero(request.StartsAt),
		EndsAt:   timeOrZero(request.EndsAt),
	})
	if s.failed(w, r, err, nil) {
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
	if s.failed(w, r, err, status.ErrNotFound) {
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
		Title: request.Title, Targets: memberRefsOf(request.Targets),
		StartsAt: timeOrZero(request.StartsAt), EndsAt: timeOrZero(request.EndsAt),
	})
	if s.failed(w, r, err, status.ErrNotFound) {
		return
	}
	s.writeAPI(w, http.StatusOK, incidentToJSON(changed))
}

func (s *Server) deleteIncident(w http.ResponseWriter, r *http.Request) {
	s.finishAPIAction(w, r, s.status.DeleteIncident(r.Context(), r.PathValue("id")), status.ErrNotFound)
}
