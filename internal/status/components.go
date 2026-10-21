package status

import (
	"context"
	"fmt"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/deletion"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/ident"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
)

func (s *Service) CreateComponent(ctx context.Context, definition ComponentDefinition) (Component, error) {
	if err := definition.Validate(); err != nil {
		return Component{}, err
	}
	id, err := ident.New()
	if err != nil {
		return Component{}, fmt.Errorf("generate id: %w", err)
	}
	component := Component{ID: id, Name: strings.TrimSpace(definition.Name), Position: definition.Position, Members: membersOf(definition.Members), CreatedAt: s.now()}
	if err := s.insertComponentUnderCeiling(ctx, component); err != nil {
		return Component{}, err
	}
	s.logger.Info("status component created", "component_id", id, "name", component.Name)
	s.Refresh(ctx)
	return s.GetComponent(ctx, id)
}

func (s *Service) insertComponentUnderCeiling(ctx context.Context, component Component) error {
	s.creating.Lock()
	defer s.creating.Unlock()
	count, err := s.store.CountComponents(ctx)
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
	current.Name = strings.TrimSpace(definition.Name)
	current.Position = definition.Position
	current.Members = membersOf(definition.Members)
	if err := s.store.UpdateComponent(ctx, current); err != nil {
		return Component{}, err
	}
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
	if err := s.store.DeleteComponent(ctx, id); err != nil {
		return err
	}
	s.logger.Info("status component deleted", "component_id", id)
	s.Refresh(ctx)
	return nil
}

// Components rend les composants tels que l'opérateur les voit : chaque
// objet nommé avec ce qu'il apporte, l'état dérivé et l'état effectif.
func (s *Service) Components(ctx context.Context) ([]Component, error) {
	components, err := s.store.ListComponents(ctx)
	if err != nil {
		return nil, err
	}
	open, err := s.store.ListIncidents(ctx, s.now())
	if err != nil {
		return nil, err
	}
	known, err := s.facts(ctx)
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
	open, err := s.store.ListIncidents(ctx, s.now())
	if err != nil {
		return Component{}, err
	}
	known, err := s.facts(ctx)
	if err != nil {
		return Component{}, err
	}
	s.judge(&component, known, open)
	return component, nil
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
