package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/service"
)

// Ce que l'écart dit d'un service déclaré : un état par service, par unité
// et par conteneur rattaché. Des faits, jamais un libellé : le front en fait
// une pastille.
type serviceGapJSON struct {
	ServiceID string        `json:"service_id"`
	State     string        `json:"state"`
	Weak      bool          `json:"weak"`
	Units     []unitGapJSON `json:"units"`
}

type unitGapJSON struct {
	UnitID     string                 `json:"unit_id"`
	Name       string                 `json:"name"`
	Kind       string                 `json:"kind"`
	State      string                 `json:"state"`
	Weak       bool                   `json:"weak"`
	Containers []containerVerdictJSON `json:"containers"`
}

type containerVerdictJSON struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	State      string `json:"state"`
	Image      string `json:"image"`
	MatchLevel string `json:"match_level"`
	Verdict    string `json:"verdict"`
	Reason     string `json:"reason"`
}

// Ce que la machine montre sans que rien ne le déclare, par clé.
type undeclaredJSON struct {
	MachineID   string                `json:"machine_id"`
	MachineName string                `json:"machine_name"`
	KeyKind     string                `json:"key_kind"`
	KeyValue    string                `json:"key_value"`
	Ignored     bool                  `json:"ignored"`
	ServiceID   string                `json:"service_id"`
	Containers  []undeclaredContainer `json:"containers"`
}

type undeclaredContainer struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	State          string `json:"state"`
	Image          string `json:"image"`
	ComposeService string `json:"compose_service"`
}

type gapsResponse struct {
	Services   []serviceGapJSON `json:"services"`
	Undeclared []undeclaredJSON `json:"undeclared"`
}

// La proposition d'adoption a la forme du corps de la création : le
// formulaire la reprend telle quelle. Avec service_id, c'est l'édition de ce
// service, qui garde ses unités et en gagne.
type proposalResponse struct {
	ServiceID  string                 `json:"service_id"`
	Definition declaredServiceRequest `json:"definition"`
}

type ignoredRequest struct {
	MachineID string `json:"machine_id"`
	Kind      string `json:"kind"`
	Value     string `json:"value"`
}

// Ce que la fiche d'un conteneur dit de l'annuaire.
type containerDirectoryJSON struct {
	Declared    bool   `json:"declared"`
	ServiceID   string `json:"service_id"`
	ServiceName string `json:"service_name"`
	UnitID      string `json:"unit_id"`
	UnitName    string `json:"unit_name"`
	MatchLevel  string `json:"match_level"`
	Weak        bool   `json:"weak"`
	KeyKind     string `json:"key_kind"`
	KeyValue    string `json:"key_value"`
	Ignored     bool   `json:"ignored"`
}

func serviceGapToJSON(gap directory.ServiceGap) serviceGapJSON {
	converted := serviceGapJSON{ServiceID: gap.ServiceID, State: string(gap.State), Weak: gap.Weak, Units: make([]unitGapJSON, 0, len(gap.Units))}
	for _, unit := range gap.Units {
		containers := make([]containerVerdictJSON, 0, len(unit.Containers))
		for _, container := range unit.Containers {
			containers = append(containers, containerVerdictJSON{
				ID: container.ServiceID, Name: container.Name, State: string(container.State), Image: container.Image,
				MatchLevel: string(container.Level), Verdict: string(container.Verdict), Reason: string(container.Reason),
			})
		}
		converted.Units = append(converted.Units, unitGapJSON{
			UnitID: unit.UnitID, Name: unit.Name, Kind: string(unit.Kind), State: string(unit.State), Weak: unit.Weak, Containers: containers,
		})
	}
	return converted
}

func undeclaredToJSON(group directory.Undeclared, machineNames map[string]string) undeclaredJSON {
	containers := make([]undeclaredContainer, 0, len(group.Containers))
	for _, container := range group.Containers {
		containers = append(containers, undeclaredContainer{
			ID: container.ID, Name: container.Name, State: string(container.State), Image: container.Image, ComposeService: container.ComposeService,
		})
	}
	return undeclaredJSON{
		MachineID: group.MachineID, MachineName: machineNames[group.MachineID], KeyKind: string(group.Key.Kind), KeyValue: group.Key.Value,
		Ignored: group.Ignored, ServiceID: group.ServiceID, Containers: containers,
	}
}

// gapsOf calcule l'écart d'un filtre et le met en JSON ; l'API et l'outil
// MCP le rendent tel quel.
func (s *Server) gapsOf(ctx context.Context, filter directory.Filter) (gapsResponse, error) {
	report, err := s.directory.Gaps(ctx, filter)
	if err != nil {
		return gapsResponse{}, err
	}
	return s.gapsResponseOf(ctx, report)
}

func (s *Server) gapsResponseOf(ctx context.Context, report directory.GapReport) (gapsResponse, error) {
	names, err := s.machineNames(ctx)
	if err != nil {
		return gapsResponse{}, err
	}
	response := gapsResponse{Services: make([]serviceGapJSON, 0, len(report.Services)), Undeclared: make([]undeclaredJSON, 0, len(report.Undeclared))}
	for _, gap := range report.Services {
		response.Services = append(response.Services, serviceGapToJSON(gap))
	}
	for _, group := range report.Undeclared {
		response.Undeclared = append(response.Undeclared, undeclaredToJSON(group, names))
	}
	return response, nil
}

func (s *Server) getDirectoryGaps(w http.ResponseWriter, r *http.Request) {
	filter := directory.Filter{MachineID: r.URL.Query().Get("machine"), ProjectID: r.URL.Query().Get("project")}
	response, err := s.gapsOf(r.Context(), filter)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusOK, response)
}

func (s *Server) getDirectoryProposal(w http.ResponseWriter, r *http.Request) {
	proposal, err := s.directory.Propose(r.Context(), r.URL.Query().Get("container"))
	switch {
	case errors.Is(err, directory.ErrContainerNotFound):
		s.apiRefuse(w, http.StatusNotFound, "directory.container_not_found")
	case errors.Is(err, directory.ErrContainerDeclared):
		s.apiRefuse(w, http.StatusConflict, "directory.container_declared")
	case errors.Is(err, directory.ErrUnitsFull):
		// Un état du service, pas une faute de saisie : 409 comme le conteneur déjà déclaré.
		s.apiRefuse(w, http.StatusConflict, "directory.units_full")
	case err != nil:
		s.apiInternalError(w, r, err)
	default:
		s.writeAPI(w, http.StatusOK, proposalResponse{ServiceID: proposal.ServiceID, Definition: declaredServiceRequestOf(proposal.Definition)})
	}
}

// declaredServiceRequestOf est l'inverse de declaredServiceDefinition : la
// définition que le serveur propose, sous la forme que le formulaire envoie.
func declaredServiceRequestOf(definition directory.ServiceDefinition) declaredServiceRequest {
	provides := make([]string, 0, len(definition.Provides))
	for _, capability := range definition.Provides {
		provides = append(provides, string(capability))
	}
	units := make([]unitRequest, 0, len(definition.Units))
	for _, unit := range definition.Units {
		units = append(units, unitRequest{
			ID: unit.ID, Name: unit.Name, Image: unit.Image, Version: unit.Version, Digest: unit.Digest,
			ContainerName: unit.ContainerName, SystemdUnit: unit.SystemdUnit, Ports: nonNil(unit.Ports),
		})
	}
	return declaredServiceRequest{
		MachineID: definition.MachineID, Name: definition.Name, Title: definition.Title, Nature: string(definition.Nature),
		Origin: string(definition.Origin), ComposeProject: definition.ComposeProject, PublicURL: definition.PublicURL,
		Provides: provides, Units: units, ProjectIDs: nonNil(definition.ProjectIDs),
	}
}

func (s *Server) ignoreKey(w http.ResponseWriter, r *http.Request) {
	var request ignoredRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	key := directory.IgnoredKey{MachineID: request.MachineID, Kind: directory.IgnoreKind(request.Kind), Value: request.Value}
	err := s.directory.Ignore(r.Context(), key)
	if refusal, refused := directoryRefusalKey(err); refused {
		s.apiRefuse(w, http.StatusUnprocessableEntity, refusal)
		return
	}
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) unignoreKey(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	key := directory.IgnoredKey{MachineID: query.Get("machine"), Kind: directory.IgnoreKind(query.Get("kind")), Value: query.Get("value")}
	err := s.directory.Unignore(r.Context(), key)
	if errors.Is(err, directory.ErrIgnoredNotFound) {
		s.apiRefuse(w, http.StatusNotFound, "directory.ignore_not_found")
		return
	}
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// containerDirectoryOf dit de la fiche d'un conteneur à quelle unité de
// l'annuaire elle se rattache, ou sinon par quelle clé on l'ignorerait.
func (s *Server) containerDirectoryOf(ctx context.Context, item service.Service) (*containerDirectoryJSON, error) {
	link, err := s.directory.ContainerLink(ctx, item)
	if err != nil {
		return nil, err
	}
	return &containerDirectoryJSON{
		Declared: link.UnitID != "", ServiceID: link.ServiceID, ServiceName: link.ServiceName,
		UnitID: link.UnitID, UnitName: link.UnitName, MatchLevel: string(link.Level), Weak: link.Level.IsWeak(),
		KeyKind: string(link.Key.Kind), KeyValue: link.Key.Value, Ignored: link.Ignored,
	}, nil
}
