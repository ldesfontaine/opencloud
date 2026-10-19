package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
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

// Ce que la machine montre sans que rien ne le déclare, par clé. Adoptable :
// au moins un conteneur du groupe n'a pas de label ; un groupe d'orphelins se
// corrige par le label, pas par l'adoption.
type undeclaredJSON struct {
	MachineID   string                `json:"machine_id"`
	MachineName string                `json:"machine_name"`
	KeyKind     string                `json:"key_kind"`
	KeyValue    string                `json:"key_value"`
	Ignored     bool                  `json:"ignored"`
	ServiceID   string                `json:"service_id"`
	Adoptable   bool                  `json:"adoptable"`
	Containers  []undeclaredContainer `json:"containers"`
}

// label_problem dit pourquoi les labels du conteneur ne le rattachent à rien ;
// vide : pas de label orphelin.
type undeclaredContainer struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	State          string `json:"state"`
	Image          string `json:"image"`
	ComposeService string `json:"compose_service"`
	LabelService   string `json:"label_service"`
	LabelUnit      string `json:"label_unit"`
	LabelProblem   string `json:"label_problem"`
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

// Ce que la fiche d'un conteneur dit de l'annuaire. Les labels sont ceux du
// conteneur ; label_problem dit, s'il n'est pas rattaché, pourquoi ils ne
// visent aucune unité de sa machine, et label_machine_name nomme la machine
// qui déclare le service visé quand c'en est une autre.
type containerDirectoryJSON struct {
	Declared         bool   `json:"declared"`
	ServiceID        string `json:"service_id"`
	ServiceName      string `json:"service_name"`
	UnitID           string `json:"unit_id"`
	UnitName         string `json:"unit_name"`
	MatchLevel       string `json:"match_level"`
	Weak             bool   `json:"weak"`
	KeyKind          string `json:"key_kind"`
	KeyValue         string `json:"key_value"`
	Ignored          bool   `json:"ignored"`
	LabelService     string `json:"label_service"`
	LabelUnit        string `json:"label_unit"`
	LabelProblem     string `json:"label_problem"`
	LabelMachineName string `json:"label_machine_name"`
}

func serviceGapToJSON(gap directory.ServiceGap) serviceGapJSON {
	converted := serviceGapJSON{ServiceID: gap.ServiceID, State: string(gap.State), Weak: gap.Weak, Units: make([]unitGapJSON, 0, len(gap.Units))}
	for _, unit := range gap.Units {
		containers := make([]containerVerdictJSON, 0, len(unit.Containers))
		for _, found := range unit.Containers {
			containers = append(containers, containerVerdictJSON{
				ID: found.ServiceID, Name: found.Name, State: string(found.State), Image: found.Image,
				MatchLevel: string(found.Level), Verdict: string(found.Verdict), Reason: string(found.Reason),
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
	for _, found := range group.Containers {
		containers = append(containers, undeclaredContainer{
			ID: found.ID, Name: found.Name, State: string(found.State), Image: found.Image, ComposeService: found.ComposeService,
			LabelService: found.Labels.Service, LabelUnit: found.Labels.Unit, LabelProblem: string(group.LabelProblems[found.ID]),
		})
	}
	return undeclaredJSON{
		MachineID: group.MachineID, MachineName: machineNames[group.MachineID], KeyKind: string(group.Key.Kind), KeyValue: group.Key.Value,
		Ignored: group.Ignored, ServiceID: group.ServiceID, Adoptable: group.Adoptable, Containers: containers,
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
	return gapsResponse{
		Services:   convertAll(report.Services, serviceGapToJSON),
		Undeclared: convertAll(report.Undeclared, func(group directory.Undeclared) undeclaredJSON { return undeclaredToJSON(group, names) }),
	}, nil
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
	case errors.Is(err, directory.ErrContainerLabelled):
		// Un état du conteneur que l'opérateur corrige par le label : 409
		// comme le conteneur déjà déclaré.
		s.apiRefuse(w, http.StatusConflict, "directory.container_labelled")
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
	provides := convertAll(definition.Provides, func(capability directory.Capability) string { return string(capability) })
	units := make([]unitRequest, 0, len(definition.Units))
	for _, unit := range definition.Units {
		units = append(units, unitRequest{
			ID: unit.ID, Name: unit.Name, Image: unit.Image, Version: unit.Version, Digest: unit.Digest,
			ContainerName: unit.ContainerName, SystemdUnit: unit.SystemdUnit, Ports: orEmpty(unit.Ports),
			UpdatePolicy: string(unit.UpdatePolicy),
		})
	}
	return declaredServiceRequest{
		MachineID: definition.MachineID, Name: definition.Name, Title: definition.Title, Nature: string(definition.Nature),
		Origin: string(definition.Origin), ComposeProject: definition.ComposeProject, PublicURL: definition.PublicURL,
		Provides: provides, Units: units, ProjectIDs: orEmpty(definition.ProjectIDs),
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
func (s *Server) containerDirectoryOf(ctx context.Context, item container.Container) (*containerDirectoryJSON, error) {
	link, err := s.directory.ContainerLink(ctx, item)
	if err != nil {
		return nil, err
	}
	return &containerDirectoryJSON{
		Declared: link.UnitID != "", ServiceID: link.ServiceID, ServiceName: link.ServiceName,
		UnitID: link.UnitID, UnitName: link.UnitName, MatchLevel: string(link.Level), Weak: link.Level.IsWeak(),
		KeyKind: string(link.Key.Kind), KeyValue: link.Key.Value, Ignored: link.Ignored,
		LabelService: link.Labels.Service, LabelUnit: link.Labels.Unit,
		LabelProblem: string(link.LabelProblem), LabelMachineName: link.LabelMachineName,
	}, nil
}
