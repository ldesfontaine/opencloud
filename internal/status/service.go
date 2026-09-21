package status

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/live"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/service"
	"github.com/ldesfontaine/opencloud/internal/settings"
)

const (
	// Les fenêtres de maintenance s'ouvrent et se ferment à la minute près,
	// et un certificat franchit son seuil sans qu'aucun sujet ne le dise.
	tickPeriod  = 30 * time.Second
	purgePeriod = 24 * time.Hour
)

// Ce que le service attend de la base ; le package store le fournit. Les
// objets sont lus tels que leurs composants les écrivent : le statut n'a
// pas de table à lui pour ce qu'il dérive.
type Store interface {
	InsertComponent(ctx context.Context, component Component) error
	UpdateComponent(ctx context.Context, component Component) error
	ListComponents(ctx context.Context) ([]Component, error)
	GetComponent(ctx context.Context, id string) (Component, error)
	DeleteComponent(ctx context.Context, id string) error
	CountComponents(ctx context.Context) (int, error)

	InsertIncident(ctx context.Context, incident Incident) error
	UpdateIncident(ctx context.Context, incident Incident) error
	// Transition écrit le nouveau statut de l'incident et l'entrée du fil
	// dans la même transaction : le fil ne ment jamais sur l'état.
	Transition(ctx context.Context, incident Incident, update Update) error
	// ListIncidents rend les incidents ouverts, et les résolus depuis
	// l'instant donné ; zéro rend tout.
	ListIncidents(ctx context.Context, resolvedSince time.Time) ([]Incident, error)
	GetIncident(ctx context.Context, id string) (Incident, error)
	DeleteIncident(ctx context.Context, id string) error
	CountOpenIncidents(ctx context.Context) (int, error)
	PurgeIncidents(ctx context.Context, resolvedBefore time.Time) error

	ListServices(ctx context.Context, machineID string) ([]service.Service, error)
	ListHeartbeats(ctx context.Context) ([]heartbeat.Heartbeat, error)
	ListProbes(ctx context.Context, machineID string) ([]probe.Probe, error)
	ListProbeDays(ctx context.Context, probeID string, from time.Time) ([]probe.Day, error)
}

// Ce que le service attend du composant machine : qui est en ligne.
type Machines interface {
	List(ctx context.Context) ([]machine.Status, error)
}

// Ce que le service attend des réglages : le titre, l'annonce et la langue
// de la page y vivent, à côté de la langue de l'interface.
type Settings interface {
	Load() (settings.Settings, error)
	Save(settings.Settings) error
}

// Watcher reçoit « la page a changé » : le bus interne pour l'administration,
// le bus public pour les visiteurs. nil est toléré.
type Watcher interface {
	StatusChanged()
}

// Feed est le bus interne, que le service écoute pour savoir quand relire :
// tout sujet peut changer l'état d'un composant.
type Feed interface {
	Subscribe() *live.Subscription
}

type Service struct {
	store    Store
	machines Machines
	settings Settings
	watchers []Watcher
	feed     Feed
	logger   *slog.Logger
	now      func() time.Time

	// digest est l'empreinte du dernier instantané publié : on ne publie
	// que ce qui change de visible.
	mu     sync.Mutex
	digest string
}

func New(store Store, machines Machines, settings Settings, logger *slog.Logger) *Service {
	return &Service{store: store, machines: machines, settings: settings, logger: logger, now: time.Now}
}

func (s *Service) SetClock(now func() time.Time) {
	s.now = now
}

func (s *Service) AddWatcher(watcher Watcher) {
	if watcher != nil {
		s.watchers = append(s.watchers, watcher)
	}
}

func (s *Service) SetFeed(feed Feed) {
	s.feed = feed
}

// --- Composants ---

func (s *Service) CreateComponent(ctx context.Context, definition ComponentDefinition) (Component, error) {
	if err := definition.Validate(); err != nil {
		return Component{}, err
	}
	count, err := s.store.CountComponents(ctx)
	if err != nil {
		return Component{}, err
	}
	if count >= MaxComponents {
		return Component{}, ErrTooMany
	}
	id, err := NewID()
	if err != nil {
		return Component{}, fmt.Errorf("generate id: %w", err)
	}
	component := Component{ID: id, Name: strings.TrimSpace(definition.Name), Position: definition.Position, Members: membersOf(definition.Members), CreatedAt: s.now()}
	if err := s.store.InsertComponent(ctx, component); err != nil {
		return Component{}, err
	}
	s.logger.Info("status component created", "component_id", id, "name", component.Name)
	s.Refresh(ctx)
	return s.GetComponent(ctx, id)
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

// facts lit tous les objets d'un coup : quatre lectures, quel que soit le
// nombre de composants.
func (s *Service) facts(ctx context.Context) (facts, error) {
	known := facts{
		machines:   map[string]machine.Status{},
		services:   map[string]service.Service{},
		heartbeats: map[string]heartbeat.Heartbeat{},
		probes:     map[string]probe.Probe{},
		now:        s.now(),
	}
	machines, err := s.machines.List(ctx)
	if err != nil {
		return facts{}, err
	}
	for _, found := range machines {
		known.machines[found.ID] = found
	}
	services, err := s.store.ListServices(ctx, "")
	if err != nil {
		return facts{}, err
	}
	for _, found := range services {
		known.services[found.ID] = found
	}
	heartbeats, err := s.store.ListHeartbeats(ctx)
	if err != nil {
		return facts{}, err
	}
	for _, found := range heartbeats {
		known.heartbeats[found.ID] = found
	}
	probes, err := s.store.ListProbes(ctx, "")
	if err != nil {
		return facts{}, err
	}
	for _, found := range probes {
		known.probes[found.ID] = found
	}
	return known, nil
}

// --- Incidents ---

// OpenIncident écrit l'incident, ses composants et la première entrée de
// son fil. Une maintenance dont la fenêtre est déjà ouverte commence tout
// de suite.
func (s *Service) OpenIncident(ctx context.Context, definition IncidentDefinition) (Incident, error) {
	now := s.now()
	if err := definition.Validate(now); err != nil {
		return Incident{}, err
	}
	if err := s.checkComponents(ctx, definition.ComponentIDs); err != nil {
		return Incident{}, err
	}
	id, err := NewID()
	if err != nil {
		return Incident{}, fmt.Errorf("generate id: %w", err)
	}
	status := definition.Status
	if definition.Impact == ImpactMaintenance && !definition.StartsAt.After(now) {
		status = StatusInProgress
	}
	incident := Incident{
		ID:        id,
		Title:     strings.TrimSpace(definition.Title),
		Impact:    definition.Impact,
		Status:    status,
		StartsAt:  definition.StartsAt,
		EndsAt:    definition.EndsAt,
		CreatedAt: now,
		UpdatedAt: now,
		Updates:   []Update{{Status: status, Message: strings.TrimSpace(definition.Message), CreatedAt: now}},
	}
	for _, componentID := range definition.ComponentIDs {
		incident.Components = append(incident.Components, ComponentRef{ID: componentID})
	}
	if err := s.store.InsertIncident(ctx, incident); err != nil {
		return Incident{}, err
	}
	s.logger.Info("incident opened", "incident_id", id, "impact", incident.Impact, "title", incident.Title)
	s.Refresh(ctx)
	return s.store.GetIncident(ctx, id)
}

// AddUpdate ajoute une entrée au fil et fait passer l'incident à ce
// statut ; « résolu » le clôt et note l'instant.
func (s *Service) AddUpdate(ctx context.Context, id string, status IncidentStatus, message string) (Incident, error) {
	incident, err := s.store.GetIncident(ctx, id)
	if err != nil {
		return Incident{}, err
	}
	if incident.IsResolved() {
		return Incident{}, ErrResolved
	}
	if !allowedStatus(incident.Impact, status) || status == StatusScheduled {
		return Incident{}, ErrStatusInvalid
	}
	if !validMessage(message) {
		return Incident{}, ErrMessageInvalid
	}
	if err := s.transition(ctx, incident, status, strings.TrimSpace(message)); err != nil {
		return Incident{}, err
	}
	s.Refresh(ctx)
	return s.store.GetIncident(ctx, id)
}

func (s *Service) transition(ctx context.Context, incident Incident, status IncidentStatus, message string) error {
	now := s.now()
	incident.Status = status
	incident.UpdatedAt = now
	if status == StatusResolved {
		incident.ResolvedAt = now
	}
	if err := s.store.Transition(ctx, incident, Update{Status: status, Message: message, CreatedAt: now}); err != nil {
		return err
	}
	s.logger.Info("incident updated", "incident_id", incident.ID, "status", status)
	return nil
}

// ChangeIncident corrige le titre, les composants et la fenêtre ; le fil
// reste tel quel.
func (s *Service) ChangeIncident(ctx context.Context, id string, change IncidentChange) (Incident, error) {
	incident, err := s.store.GetIncident(ctx, id)
	if err != nil {
		return Incident{}, err
	}
	if !validText(change.Title, MaxTitleLength) {
		return Incident{}, ErrTitleInvalid
	}
	if len(change.ComponentIDs) == 0 || len(change.ComponentIDs) > MaxComponents {
		return Incident{}, ErrComponentInvalid
	}
	if err := validateWindow(incident.Impact, change.StartsAt, change.EndsAt, s.now()); err != nil {
		return Incident{}, err
	}
	if err := s.checkComponents(ctx, change.ComponentIDs); err != nil {
		return Incident{}, err
	}
	incident.Title = strings.TrimSpace(change.Title)
	incident.StartsAt = change.StartsAt
	incident.EndsAt = change.EndsAt
	incident.UpdatedAt = s.now()
	incident.Components = incident.Components[:0]
	for _, componentID := range change.ComponentIDs {
		incident.Components = append(incident.Components, ComponentRef{ID: componentID})
	}
	if err := s.store.UpdateIncident(ctx, incident); err != nil {
		return Incident{}, err
	}
	s.Refresh(ctx)
	return s.store.GetIncident(ctx, id)
}

func (s *Service) DeleteIncident(ctx context.Context, id string) error {
	if err := s.store.DeleteIncident(ctx, id); err != nil {
		return err
	}
	s.logger.Info("incident deleted", "incident_id", id)
	s.Refresh(ctx)
	return nil
}

// Incidents rend tout ce que la base garde, du plus récent au plus ancien.
func (s *Service) Incidents(ctx context.Context) ([]Incident, error) {
	return s.store.ListIncidents(ctx, time.Time{})
}

func (s *Service) GetIncident(ctx context.Context, id string) (Incident, error) {
	return s.store.GetIncident(ctx, id)
}

func (s *Service) CountOpenIncidents(ctx context.Context) (int, error) {
	return s.store.CountOpenIncidents(ctx)
}

// checkComponents refuse un identifiant qui n'est pas un composant, ou
// donné deux fois.
func (s *Service) checkComponents(ctx context.Context, ids []string) error {
	components, err := s.store.ListComponents(ctx)
	if err != nil {
		return err
	}
	known := make(map[string]bool, len(components))
	for _, component := range components {
		known[component.ID] = true
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !known[id] || seen[id] {
			return ErrComponentInvalid
		}
		seen[id] = true
	}
	return nil
}

// --- La page ---

// Page rend les réglages de la page, la langue résolue : celle du réglage,
// sinon celle de l'interface.
func (s *Service) Page() (Page, error) {
	current, err := s.settings.Load()
	if err != nil {
		return Page{}, err
	}
	return pageOf(current), nil
}

func pageOf(current settings.Settings) Page {
	language := current.StatusLanguage
	if language == "" {
		language = current.Language
	}
	if language == "" {
		language = lang.Default
	}
	return Page{Title: current.StatusTitle, Announcement: current.StatusAnnouncement, Language: language}
}

// SetPage relit les réglages avant d'écrire : la langue de l'interface, que
// le serveur tient, n'est jamais écrasée par une copie périmée.
func (s *Service) SetPage(ctx context.Context, page Page) (Page, error) {
	if err := page.Validate(); err != nil {
		return Page{}, err
	}
	current, err := s.settings.Load()
	if err != nil {
		return Page{}, err
	}
	current.StatusTitle = strings.TrimSpace(page.Title)
	current.StatusAnnouncement = strings.TrimSpace(page.Announcement)
	current.StatusLanguage = page.Language
	if err := s.settings.Save(current); err != nil {
		return Page{}, err
	}
	s.Refresh(ctx)
	return pageOf(current), nil
}

// --- L'instantané public ---

// Snapshot est ce que le public voit. Un composant dont aucun objet ne
// compte et qu'aucun incident ne touche n'y est pas : rien à en dire.
func (s *Service) Snapshot(ctx context.Context) (Snapshot, error) {
	now := s.now()
	page, err := s.Page()
	if err != nil {
		return Snapshot{}, err
	}
	components, err := s.Components(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	incidents, err := s.store.ListIncidents(ctx, now.Add(-HistorySpan))
	if err != nil {
		return Snapshot{}, err
	}
	days, err := s.store.ListProbeDays(ctx, "", startOfDay(now.Add(-UptimeSpan)))
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{Page: page, GeneratedAt: now, Components: []PublicComponent{}, Open: []Incident{}, History: []Incident{}}
	states := []State{}
	for _, component := range components {
		if component.Effective == StateUnknown {
			continue
		}
		states = append(states, component.Effective)
		snapshot.Components = append(snapshot.Components, PublicComponent{
			ID:    component.ID,
			Name:  component.Name,
			State: component.Effective,
			Days:  uptimeOf(component, days),
		})
	}
	snapshot.Global = Worst(states)
	for _, incident := range incidents {
		if incident.IsResolved() {
			snapshot.History = append(snapshot.History, incident)
		} else {
			snapshot.Open = append(snapshot.Open, incident)
		}
	}
	return snapshot, nil
}

// uptimeOf additionne, jour par jour, les jours des sondes du composant ;
// rien pour un composant sans sonde.
func uptimeOf(component Component, days []probe.Day) []Day {
	probes := map[string]bool{}
	for _, member := range component.Members {
		if member.Kind == KindProbe && member.Present {
			probes[member.ID] = true
		}
	}
	if len(probes) == 0 {
		return nil
	}
	byDay := map[time.Time]*Day{}
	for _, day := range days {
		if !probes[day.ProbeID] {
			continue
		}
		key := day.Day.UTC()
		summed, ok := byDay[key]
		if !ok {
			summed = &Day{Day: key}
			byDay[key] = summed
		}
		summed.Total += day.Total
		summed.Success += day.Success
		summed.Degraded += day.Degraded
	}
	result := make([]Day, 0, len(byDay))
	for _, day := range byDay {
		result = append(result, *day)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Day.Before(result[j].Day) })
	return result
}

func startOfDay(at time.Time) time.Time {
	at = at.UTC()
	return time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
}

// --- Le direct ---

// Refresh relit l'instantané et prévient si quelque chose de visible a
// changé. Appelé après chaque écriture de l'opérateur, à chaque sujet du
// bus interne, et toutes les trente secondes.
func (s *Service) Refresh(ctx context.Context) {
	snapshot, err := s.Snapshot(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.Error("status snapshot", "error", err)
		}
		return
	}
	digest := digestOf(snapshot)
	s.mu.Lock()
	changed := digest != s.digest
	s.digest = digest
	s.mu.Unlock()
	if !changed {
		return
	}
	for _, watcher := range s.watchers {
		watcher.StatusChanged()
	}
}

// digestOf résume ce que le visiteur voit ; l'instant de génération et les
// jours d'uptime n'en font pas partie, sinon chaque rollup réveillerait la
// page pour une barre qui n'a pas bougé à l'œil.
func digestOf(snapshot Snapshot) string {
	type component struct {
		ID    string
		Name  string
		State State
	}
	type incident struct {
		ID         string
		Status     IncidentStatus
		Title      string
		UpdatedAt  time.Time
		Components []ComponentRef
	}
	summary := struct {
		Page       Page
		Global     State
		Components []component
		Incidents  []incident
	}{Page: snapshot.Page, Global: snapshot.Global}
	for _, found := range snapshot.Components {
		summary.Components = append(summary.Components, component{found.ID, found.Name, found.State})
	}
	for _, found := range append(append([]Incident{}, snapshot.Open...), snapshot.History...) {
		summary.Incidents = append(summary.Incidents, incident{found.ID, found.Status, found.Title, found.UpdatedAt, found.Components})
	}
	encoded, _ := json.Marshal(summary)
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

// Watch tient la page à jour : relit à chaque sujet du bus interne, ouvre
// et ferme les maintenances à l'heure, purge une fois par jour.
func (s *Service) Watch(ctx context.Context) {
	if err := s.Purge(ctx); err != nil {
		s.logger.Warn("purge incidents", "error", err)
	}
	s.Refresh(ctx)
	topics := s.listen(ctx)
	tick := time.NewTicker(tickPeriod)
	defer tick.Stop()
	purge := time.NewTicker(purgePeriod)
	defer purge.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.ApplyWindows(ctx)
			s.Refresh(ctx)
		case <-purge.C:
			if err := s.Purge(ctx); err != nil {
				s.logger.Warn("purge incidents", "error", err)
			}
		case _, open := <-topics:
			if !open {
				return
			}
			s.Refresh(ctx)
		}
	}
}

// listen rend un canal qui se réveille à chaque lot de sujets, sauf quand
// le lot ne porte que « status », qui est le nôtre.
func (s *Service) listen(ctx context.Context) <-chan struct{} {
	wake := make(chan struct{})
	if s.feed == nil {
		return wake
	}
	subscription := s.feed.Subscribe()
	go func() {
		defer close(wake)
		defer subscription.Close()
		for {
			batch, err := subscription.Next(ctx)
			if err != nil {
				return
			}
			if onlyStatus(batch) {
				continue
			}
			select {
			case wake <- struct{}{}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return wake
}

func onlyStatus(batch []live.Topic) bool {
	for _, topic := range batch {
		if topic != live.TopicStatus {
			return false
		}
	}
	return true
}

// ApplyWindows ouvre les maintenances dont l'heure est venue et résout
// celles dont la fenêtre est passée.
func (s *Service) ApplyWindows(ctx context.Context) {
	now := s.now()
	incidents, err := s.store.ListIncidents(ctx, now)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.Error("list incidents", "error", err)
		}
		return
	}
	for _, incident := range incidents {
		if !incident.IsMaintenance() || incident.IsResolved() {
			continue
		}
		switch {
		case incident.Status == StatusScheduled && !incident.StartsAt.After(now):
			err = s.transition(ctx, incident, StatusInProgress, "")
		case incident.Status == StatusInProgress && !incident.EndsAt.After(now):
			err = s.transition(ctx, incident, StatusResolved, "")
		}
		if err != nil && ctx.Err() == nil {
			s.logger.Error("apply maintenance window", "incident_id", incident.ID, "error", err)
		}
	}
}

// Purge efface les incidents résolus depuis plus d'un an, fil et
// rattachements avec eux.
func (s *Service) Purge(ctx context.Context) error {
	return s.store.PurgeIncidents(ctx, s.now().Add(-IncidentRetention))
}
