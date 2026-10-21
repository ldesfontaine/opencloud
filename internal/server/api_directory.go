package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
)

// Un service déclaré tel que le front le lit : des faits, les listes
// fermées en mots ; les libellés se traduisent dans le navigateur.
type declaredServiceJSON struct {
	ID             string     `json:"id"`
	MachineID      string     `json:"machine_id"`
	MachineName    string     `json:"machine_name"`
	Name           string     `json:"name"`
	Title          string     `json:"title"`
	Nature         string     `json:"nature"`
	Origin         string     `json:"origin"`
	ComposeProject string     `json:"compose_project"`
	PublicURL      string     `json:"public_url"`
	DeclaredBy     string     `json:"declared_by"`
	NeedsReview    bool       `json:"needs_review"`
	Provides       []string   `json:"provides"`
	Units          []unitJSON `json:"units"`
	ProjectIDs     []string   `json:"project_ids"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type unitJSON struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Kind          string   `json:"kind"`
	Image         string   `json:"image"`
	Version       string   `json:"version"`
	Digest        string   `json:"digest"`
	ContainerName string   `json:"container_name"`
	SystemdUnit   string   `json:"systemd_unit"`
	Ports         []string `json:"ports"`
	// Ce que l'opérateur veut des mises à jour de l'image : watch, hold,
	// ignore.
	UpdatePolicy string `json:"update_policy"`
}

// La fiche d'un service déclaré dit en plus ce qui le lie : tant que des
// sondes ou des composants de statut le visent, il ne se supprime pas.
type declaredServiceDetailJSON struct {
	declaredServiceJSON
	Links linksJSON `json:"links"`
}

type linksJSON struct {
	Probes     []linkedObjectJSON `json:"probes"`
	Components []linkedObjectJSON `json:"components"`
}

type linkedObjectJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Le refus de supprimer un service lié : la clé du catalogue, et la liste de
// ce qui le lie, seule réponse d'erreur qui porte plus qu'une clé.
type linkedRefusalJSON struct {
	Error string    `json:"error"`
	Links linksJSON `json:"links"`
}

type projectJSON struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Title      string    `json:"title"`
	ServiceIDs []string  `json:"service_ids"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type declaredServicesResponse struct {
	Services []declaredServiceJSON `json:"services"`
}

type projectsResponse struct {
	Projects []projectJSON `json:"projects"`
}

// Le corps d'une création ou d'une édition : ce que l'opérateur dit, sans
// ce que le serveur pose (identifiant, provenance, dates). Une unité qui
// garde son identifiant reste la même.
type declaredServiceRequest struct {
	MachineID      string        `json:"machine_id"`
	Name           string        `json:"name"`
	Title          string        `json:"title"`
	Nature         string        `json:"nature"`
	Origin         string        `json:"origin"`
	ComposeProject string        `json:"compose_project"`
	PublicURL      string        `json:"public_url"`
	Provides       []string      `json:"provides"`
	Units          []unitRequest `json:"units"`
	ProjectIDs     []string      `json:"project_ids"`
}

type unitRequest struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Image         string   `json:"image"`
	Version       string   `json:"version"`
	Digest        string   `json:"digest"`
	ContainerName string   `json:"container_name"`
	SystemdUnit   string   `json:"systemd_unit"`
	Ports         []string `json:"ports"`
	// Absente : watch.
	UpdatePolicy string `json:"update_policy,omitempty"`
}

type unitPolicyRequest struct {
	Policy string `json:"policy"`
}

type projectRequest struct {
	Name  string `json:"name"`
	Title string `json:"title"`
}

func declaredServiceToJSON(found directory.Service) declaredServiceJSON {
	provides := httpx.ConvertAll(found.Provides, func(capability directory.Capability) string { return string(capability) })
	units := httpx.ConvertAll(found.Units, unitToJSON)
	return declaredServiceJSON{
		ID:             found.ID,
		MachineID:      found.MachineID,
		MachineName:    found.MachineName,
		Name:           found.Name,
		Title:          found.Title,
		Nature:         string(found.Nature),
		Origin:         string(found.Origin),
		ComposeProject: found.ComposeProject,
		PublicURL:      found.PublicURL,
		DeclaredBy:     string(found.DeclaredBy),
		NeedsReview:    found.NeedsReview,
		Provides:       provides,
		Units:          units,
		ProjectIDs:     orEmpty(found.ProjectIDs),
		CreatedAt:      found.CreatedAt.UTC(),
		UpdatedAt:      found.UpdatedAt.UTC(),
	}
}

func unitToJSON(unit directory.Unit) unitJSON {
	return unitJSON{
		ID:            unit.ID,
		Name:          unit.Name,
		Kind:          string(unit.Kind),
		Image:         unit.Image,
		Version:       unit.Version,
		Digest:        unit.Digest,
		ContainerName: unit.ContainerName,
		SystemdUnit:   unit.SystemdUnit,
		Ports:         orEmpty(unit.Ports),
		UpdatePolicy:  string(unit.UpdatePolicy),
	}
}

func linksToJSON(links directory.Links) linksJSON {
	return linksJSON{Probes: linkedObjectsToJSON(links.Probes), Components: linkedObjectsToJSON(links.Components)}
}

func linkedObjectsToJSON(objects []directory.LinkedObject) []linkedObjectJSON {
	return httpx.ConvertAll(objects, func(object directory.LinkedObject) linkedObjectJSON {
		return linkedObjectJSON{ID: object.ID, Name: object.Name}
	})
}

func projectToJSON(found directory.Project) projectJSON {
	return projectJSON{
		ID:         found.ID,
		Name:       found.Name,
		Title:      found.Title,
		ServiceIDs: orEmpty(found.ServiceIDs),
		CreatedAt:  found.CreatedAt.UTC(),
		UpdatedAt:  found.UpdatedAt.UTC(),
	}
}

func declaredServiceDefinition(request declaredServiceRequest) directory.ServiceDefinition {
	provides := httpx.ConvertAll(request.Provides, func(capability string) directory.Capability { return directory.Capability(capability) })
	units := make([]directory.UnitDefinition, 0, len(request.Units))
	for _, unit := range request.Units {
		units = append(units, directory.UnitDefinition{
			ID:            unit.ID,
			Name:          unit.Name,
			Image:         unit.Image,
			Version:       unit.Version,
			Digest:        unit.Digest,
			ContainerName: unit.ContainerName,
			SystemdUnit:   unit.SystemdUnit,
			Ports:         unit.Ports,
			UpdatePolicy:  container.UpdatePolicy(unit.UpdatePolicy),
		})
	}
	return directory.ServiceDefinition{
		MachineID:      request.MachineID,
		Name:           request.Name,
		Title:          request.Title,
		Nature:         directory.Nature(request.Nature),
		Origin:         directory.Origin(request.Origin),
		ComposeProject: request.ComposeProject,
		PublicURL:      request.PublicURL,
		Provides:       provides,
		Units:          units,
		ProjectIDs:     request.ProjectIDs,
	}
}

func (s *Server) listDeclaredServices(w http.ResponseWriter, r *http.Request) {
	filter := directory.Filter{MachineID: r.URL.Query().Get("machine"), ProjectID: r.URL.Query().Get("project")}
	found, err := s.reader.ListDeclaredServices(r.Context(), filter)
	if s.failed(w, r, err, nil) {
		return
	}
	response := declaredServicesResponse{Services: httpx.ConvertAll(found, declaredServiceToJSON)}
	s.writeAPI(w, http.StatusOK, response)
}

func (s *Server) getDeclaredService(w http.ResponseWriter, r *http.Request) {
	found, err := s.reader.GetDeclaredService(r.Context(), r.PathValue("id"))
	if s.failed(w, r, err, directory.ErrNotFound) {
		return
	}
	links, err := s.reader.ListServiceLinks(r.Context(), found.ID)
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, declaredServiceDetailJSON{declaredServiceJSON: declaredServiceToJSON(found), Links: linksToJSON(links)})
}

// L'opérateur déclare le service, ses unités et ce qu'il fournit ; le
// composant valide et répond par la clé du refus.
func (s *Server) createDeclaredService(w http.ResponseWriter, r *http.Request) {
	var request declaredServiceRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	created, err := s.directory.CreateService(r.Context(), declaredServiceDefinition(request))
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusCreated, declaredServiceToJSON(created))
}

// L'édition remplace la définition entière : les unités dont le corps
// redonne l'identifiant restent, celles qu'il omet sont supprimées.
func (s *Server) updateDeclaredService(w http.ResponseWriter, r *http.Request) {
	var request declaredServiceRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	updated, err := s.directory.UpdateService(r.Context(), r.PathValue("id"), declaredServiceDefinition(request))
	if s.failed(w, r, err, directory.ErrNotFound) {
		return
	}
	s.writeAPI(w, http.StatusOK, declaredServiceToJSON(updated))
}

// Supprimer un service que des sondes ou des composants de statut visent
// encore est refusé, avec la liste : l'opérateur les détache d'abord.
func (s *Server) deleteDeclaredService(w http.ResponseWriter, r *http.Request) {
	err := s.directory.DeleteService(r.Context(), r.PathValue("id"))
	var linked *directory.LinkedError
	if errors.As(err, &linked) {
		s.writeAPI(w, http.StatusConflict, linkedRefusalJSON{Error: "directory.service_linked", Links: linksToJSON(linked.Links)})
		return
	}
	s.finishAPIAction(w, r, err, directory.ErrNotFound)
}

// Valider dit que l'opérateur a relu un service repris de l'existant. Une
// définition qui ne passe pas la validation ne se valide pas : le refus est
// celui de la première règle violée.
func (s *Server) reviewDeclaredService(w http.ResponseWriter, r *http.Request) {
	reviewed, err := s.directory.Review(r.Context(), r.PathValue("id"))
	if s.failed(w, r, err, directory.ErrNotFound) {
		return
	}
	s.writeAPI(w, http.StatusOK, declaredServiceToJSON(reviewed))
}

// La politique de mise à jour se règle sur l'unité : ses conteneurs, ceux
// qui seront recréés aussi, la suivent.
func (s *Server) setUnitUpdatePolicy(w http.ResponseWriter, r *http.Request) {
	var request unitPolicyRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	_, err := s.directory.SetUnitUpdatePolicy(r.Context(), r.PathValue("id"), container.UpdatePolicy(request.Policy))
	if errors.Is(err, directory.ErrUnitNotFound) {
		s.apiRefuse(w, http.StatusNotFound, "directory.unit_not_found")
		return
	}
	if s.failed(w, r, err, nil) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	found, err := s.reader.ListProjects(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	response := projectsResponse{Projects: httpx.ConvertAll(found, projectToJSON)}
	s.writeAPI(w, http.StatusOK, response)
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	found, err := s.reader.GetProject(r.Context(), r.PathValue("id"))
	if s.failed(w, r, err, directory.ErrProjectNotFound) {
		return
	}
	s.writeAPI(w, http.StatusOK, projectToJSON(found))
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var request projectRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	created, err := s.directory.CreateProject(r.Context(), directory.ProjectDefinition{Name: request.Name, Title: request.Title})
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusCreated, projectToJSON(created))
}

func (s *Server) updateProject(w http.ResponseWriter, r *http.Request) {
	var request projectRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	updated, err := s.directory.UpdateProject(r.Context(), r.PathValue("id"), directory.ProjectDefinition{Name: request.Name, Title: request.Title})
	if s.failed(w, r, err, directory.ErrProjectNotFound) {
		return
	}
	s.writeAPI(w, http.StatusOK, projectToJSON(updated))
}

// Supprimer un projet défait le groupe : ses services restent déclarés.
func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	s.finishAPIAction(w, r, s.directory.DeleteProject(r.Context(), r.PathValue("id")), directory.ErrProjectNotFound)
}
