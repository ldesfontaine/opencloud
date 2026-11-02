package hostconn

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/link"
)

// MaxSightingsPerMachine borne ce qu'une machine garde en mémoire : un agent
// ne remplit pas le serveur. Au-delà, les vues les plus anciennes partent.
const MaxSightingsPerMachine = 2000

// Watcher reçoit chaque nouveauté visible : le direct s'y branche. nil est
// toléré.
type Watcher interface {
	DirectoryChanged()
}

// Service garde ce que les agents ont vu joindre les ports de leur hôte.
//
// Rien n'est écrit en base (aucune migration : la liste des signaux de la
// table des liens est fermée, et un lien observé n'est qu'une vue). Chaque
// rapport avance la dernière vue de ce qu'il porte ; ce qu'il ne porte plus
// vieillit sans disparaître (« l'absence d'observation n'est pas l'absence de
// lien », étude §3.2). Un redémarrage du serveur efface la mémoire, et chaque
// agent redit son rapport à l'ouverture de son flux.
type Service struct {
	mu      sync.Mutex
	seen    map[string]map[key]*link.Sighting
	watcher Watcher
	now     func() time.Time
}

type key struct {
	source string
	port   int
}

func New() *Service {
	return &Service{seen: map[string]map[key]*link.Sighting{}, now: time.Now}
}

func (s *Service) SetClock(now func() time.Time) {
	s.now = now
}

func (s *Service) SetWatcher(watcher Watcher) {
	s.watcher = watcher
}

// Record garde le rapport d'une machine. Un rapport hors bornes est refusé en
// entier. Rien n'est journalisé à l'audit : c'est une lecture de l'agent, comme
// ses mesures.
func (s *Service) Record(_ context.Context, machineID string, report Report) error {
	if err := report.Validate(); err != nil {
		return err
	}
	if s.update(machineID, report) && s.watcher != nil {
		s.watcher.DirectoryChanged()
	}
	return nil
}

// update avance les vues et dit si une source est apparue.
func (s *Service) update(machineID string, report Report) (appeared bool) {
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	sightings := s.seen[machineID]
	if sightings == nil {
		sightings = map[key]*link.Sighting{}
		s.seen[machineID] = sightings
	}
	for _, connection := range report.Connections {
		id := key{source: sourceKey(connection), port: connection.Port}
		if existing := sightings[id]; existing != nil {
			existing.LastSeen = now
			continue
		}
		sightings[id] = &link.Sighting{Source: id.source, Port: id.port, FirstSeen: now, LastSeen: now}
		appeared = true
	}
	if len(sightings) > MaxSightingsPerMachine {
		dropOldest(sightings)
	}
	return appeared
}

// dropOldest retire les vues les plus anciennes jusqu'au plafond.
func dropOldest(sightings map[key]*link.Sighting) {
	ids := make([]key, 0, len(sightings))
	for id := range sightings {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b key) int {
		if byTime := sightings[a].LastSeen.Compare(sightings[b].LastSeen); byTime != 0 {
			return byTime
		}
		return strings.Compare(a.source, b.source)
	})
	for _, id := range ids[:len(ids)-MaxSightingsPerMachine] {
		delete(sightings, id)
	}
}

// Seen rend ce que la machine a vu, par port puis par source.
func (s *Service) Seen(machineID string) []link.Sighting {
	s.mu.Lock()
	defer s.mu.Unlock()
	found := make([]link.Sighting, 0, len(s.seen[machineID]))
	for _, sighting := range s.seen[machineID] {
		found = append(found, *sighting)
	}
	slices.SortFunc(found, func(a, b link.Sighting) int {
		if a.Port != b.Port {
			return a.Port - b.Port
		}
		return strings.Compare(a.Source, b.Source)
	})
	return found
}

// sourceKey rend la clé d'instance de la source d'une connexion.
func sourceKey(connection Connection) string {
	switch connection.Origin {
	case OriginAccount:
		return link.AccountKey(connection.Name)
	case OriginAddress:
		return link.AddressKey(connection.Name)
	default:
		return link.InternetKey()
	}
}
