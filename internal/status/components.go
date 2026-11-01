package status

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/deletion"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/ident"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
)

// CreateComponent pose le composant sur son projet, qui doit exister.
func (s *Service) CreateComponent(ctx context.Context, definition ComponentDefinition) (Component, error) {
	if definition.ProjectID == "" {
		return Component{}, ErrProjectInvalid
	}
	if err := definition.Validate(); err != nil {
		return Component{}, err
	}
	id, err := ident.New()
	if err != nil {
		return Component{}, fmt.Errorf("generate id: %w", err)
	}
	component := Component{
		ID: id, ProjectID: definition.ProjectID, Name: strings.TrimSpace(definition.Name), Position: definition.Position,
		Members: membersOf(definition.Members), CreatedAt: s.now(),
	}
	if err := s.insertComponentUnderCeiling(ctx, component); err != nil {
		return Component{}, err
	}
	s.logger.Info("status component created", "component_id", id, "name", component.Name)
	s.auditor.Record(ctx, componentEvent(audit.StatusComponentCreate, component))
	s.Refresh(ctx)
	return s.GetComponent(ctx, id)
}

func (s *Service) insertComponentUnderCeiling(ctx context.Context, component Component) error {
	s.creating.Lock()
	defer s.creating.Unlock()
	count, err := s.store.CountComponents(ctx, component.ProjectID)
	if err != nil {
		return err
	}
	if count >= MaxComponents {
		return ErrTooMany
	}
	return s.store.InsertComponent(ctx, component)
}

func (s *Service) UpdateComponent(ctx context.Context, id string, definition ComponentDefinition) (Component, error) {
	if err := definition.Validate(); err != nil {
		return Component{}, err
	}
	current, err := s.store.GetComponent(ctx, id)
	if err != nil {
		return Component{}, err
	}
	// Le projet ne change pas : un composant n'existe que sur le sien.
	current.Name = strings.TrimSpace(definition.Name)
	current.Position = definition.Position
	current.Members = membersOf(definition.Members)
	if err := s.store.UpdateComponent(ctx, current); err != nil {
		return Component{}, err
	}
	s.auditor.Record(ctx, componentEvent(audit.StatusComponentUpdate, current))
	s.Refresh(ctx)
	return s.GetComponent(ctx, id)
}

func membersOf(refs []MemberRef) []Member {
	members := make([]Member, 0, len(refs))
	for _, ref := range refs {
		members = append(members, Member{Kind: ref.Kind, ID: ref.ID})
	}
	return members
}

// DeleteComponentPlan dit ce que DeleteComponent ferait : rien ne le retient,
// ses membres partent, les incidents qui le citaient restent.
func (s *Service) DeleteComponentPlan(ctx context.Context, id string) (deletion.Plan, error) {
	if _, err := s.store.GetComponent(ctx, id); err != nil {
		return deletion.Plan{}, err
	}
	return s.store.ComponentDeletionPlan(ctx, id)
}

func (s *Service) DeleteComponent(ctx context.Context, id string) error {
	// Relu avant : le journal garde le nom de ce qui part.
	found, err := s.store.GetComponent(ctx, id)
	if err != nil {
		return err
	}
	if err := s.store.DeleteComponent(ctx, id); err != nil {
		return err
	}
	s.logger.Info("status component deleted", "component_id", id)
	s.auditor.Record(ctx, componentEvent(audit.StatusComponentDelete, found))
	s.Refresh(ctx)
	return nil
}

// Components rend les composants d'un projet tels que l'opérateur les voit :
// chaque objet nommé avec ce qu'il apporte, l'état dérivé et l'état
// effectif. Un projet publié sans composant saisi en montre un par service,
// marqués implicites. Un projet vide les rend tous, tous projets confondus.
func (s *Service) Components(ctx context.Context, projectID string) ([]Component, error) {
	known, err := s.facts(ctx)
	if err != nil {
		return nil, err
	}
	all, err := s.componentsOf(ctx, known)
	if err != nil {
		return nil, err
	}
	components := []Component{}
	for _, component := range all {
		if projectID == "" || component.ProjectID == projectID {
			components = append(components, component)
		}
	}
	open, err := s.readIncidents(ctx, known, all, s.now())
	if err != nil {
		return nil, err
	}
	for i := range components {
		s.judge(&components[i], known, open)
	}
	return components, nil
}

func (s *Service) GetComponent(ctx context.Context, id string) (Component, error) {
	component, err := s.store.GetComponent(ctx, id)
	if err != nil {
		return Component{}, err
	}
	known, err := s.facts(ctx)
	if err != nil {
		return Component{}, err
	}
	open, err := s.readIncidents(ctx, known, []Component{component}, s.now())
	if err != nil {
		return Component{}, err
	}
	s.judge(&component, known, open)
	return component, nil
}

// readIncidents rend les incidents ouverts et les résolus depuis l'instant
// donné, plus les silences annoncés, chacun avec ceux des composants donnés
// que ses cibles touchent.
func (s *Service) readIncidents(ctx context.Context, known facts, components []Component, resolvedSince time.Time) ([]Incident, error) {
	written, err := s.store.ListIncidents(ctx, resolvedSince)
	if err != nil {
		return nil, err
	}
	incidents, err := s.withAnnounced(ctx, written, s.now())
	if err != nil {
		return nil, err
	}
	known.describe(incidents, components)
	return incidents, nil
}

// judge remplit les membres et pose les deux états du composant.
func (s *Service) judge(component *Component, known facts, incidents []Incident) {
	for i := range component.Members {
		known.fill(&component.Members[i])
	}
	component.Derived = derive(component.Members)
	component.Effective = effective(component.Derived, override(component.ID, incidents))
}

// facts lit tous les objets d'un coup : cinq lectures, quel que soit le
// nombre de composants.
func (s *Service) facts(ctx context.Context) (facts, error) {
	known := facts{
		machines:         map[string]machine.Status{},
		declared:         map[string]directory.Service{},
		containersByUnit: map[string][]container.Container{},
		heartbeats:       map[string]heartbeat.Heartbeat{},
		probes:           map[string]probe.Probe{},
		now:              s.now(),
	}
	machines, err := s.machines.List(ctx)
	if err != nil {
		return facts{}, err
	}
	for _, found := range machines {
		known.machines[found.ID] = found
	}
	declared, err := s.reader.ListDeclaredServices(ctx, directory.Filter{})
	if err != nil {
		return facts{}, err
	}
	for _, found := range declared {
		known.declared[found.ID] = found
	}
	containers, err := s.reader.ListContainers(ctx, "")
	if err != nil {
		return facts{}, err
	}
	for _, found := range containers {
		if !found.IsArchived() && found.UnitID != "" {
			known.containersByUnit[found.UnitID] = append(known.containersByUnit[found.UnitID], found)
		}
	}
	heartbeats, err := s.reader.ListHeartbeats(ctx)
	if err != nil {
		return facts{}, err
	}
	for _, found := range heartbeats {
		known.heartbeats[found.ID] = found
	}
	probes, err := s.reader.ListProbes(ctx, "")
	if err != nil {
		return facts{}, err
	}
	for _, found := range probes {
		known.probes[found.ID] = found
	}
	return known, nil
}

// componentsOf rend les composants de tous les projets : ceux que l'opérateur
// a saisis, et, pour un projet publié qui n'en a aucun, un composant par
// service, nommé comme le service. Rien de cela n'est écrit : ajouter un
// service au projet ajoute son composant, saisir un premier composant les
// remplace tous.
func (s *Service) componentsOf(ctx context.Context, known facts) ([]Component, error) {
	components, err := s.store.ListComponents(ctx, "")
	if err != nil {
		return nil, err
	}
	projects, err := s.reader.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	hasComponents := map[string]bool{}
	for _, component := range components {
		hasComponents[component.ProjectID] = true
	}
	for _, project := range projects {
		if !project.Publication.IsPublished() || hasComponents[project.ID] {
			continue
		}
		components = append(components, implicitComponents(project, known)...)
	}
	return components, nil
}

// implicitComponents : un composant par service du projet, dans l'ordre où
// l'annuaire les range. Son identifiant se dérive du projet et du service sans
// les nommer : il sort en public avec l'instantané.
func implicitComponents(project directory.Project, known facts) []Component {
	components := []Component{}
	for position, serviceID := range project.ServiceIDs {
		service, ok := known.declared[serviceID]
		if !ok {
			continue
		}
		name := service.Title
		if name == "" {
			name = service.Name
		}
		components = append(components, Component{
			ID:        implicitID(project.ID, serviceID),
			ProjectID: project.ID,
			Name:      name,
			Position:  position,
			Members:   []Member{{Kind: KindService, ID: serviceID}},
			CreatedAt: project.CreatedAt,
			Implicit:  true,
		})
	}
	return components
}

func implicitID(projectID, serviceID string) string {
	sum := sha256.Sum256([]byte(projectID + "/" + serviceID))
	return "auto_" + hex.EncodeToString(sum[:8])
}
