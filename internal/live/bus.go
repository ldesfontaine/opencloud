package live

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Topic nomme ce qui a changé ; le front relit la ressource qui va avec.
type Topic string

const (
	TopicMachines   Topic = "machines"
	TopicJobs       Topic = "jobs"
	TopicResources  Topic = "resources"
	TopicContainers Topic = "containers"
	TopicProbes     Topic = "probes"
	TopicStatus     Topic = "status"
	TopicAlerts     Topic = "alerts"
	TopicDirectory  Topic = "directory"
)

// ResourcesEvery espace les publications des mesures : chaque machine en
// écrit un lot toutes les 10 à 30 s, et l'écran n'a pas besoin de plus
// d'une relecture toutes les 30 s, quel que soit le nombre de machines.
const ResourcesEvery = 30 * time.Second

type Bus struct {
	mu          sync.Mutex
	subscribers map[*Subscription]struct{}
	// Les sujets espacés : leur dernière publication, et ceux dont la
	// prochaine est déjà prévue.
	lastPublished map[Topic]time.Time
	scheduled     map[Topic]bool
}

// Subscription est l'abonnement d'un onglet : les sujets en attente, et un
// réveil à une place. Publier deux fois le même sujet avant que l'abonné
// ne lise ne fait qu'un sujet : c'est ce qui protège un client lent.
type Subscription struct {
	bus     *Bus
	mu      sync.Mutex
	pending map[Topic]struct{}
	wake    chan struct{}
}

func New() *Bus {
	return &Bus{
		subscribers:   make(map[*Subscription]struct{}),
		lastPublished: make(map[Topic]time.Time),
		scheduled:     make(map[Topic]bool),
	}
}

func (b *Bus) Subscribe() *Subscription {
	subscription := &Subscription{
		bus:     b,
		pending: make(map[Topic]struct{}),
		wake:    make(chan struct{}, 1),
	}
	b.mu.Lock()
	b.subscribers[subscription] = struct{}{}
	b.mu.Unlock()
	return subscription
}

// Publish marque le sujet chez chaque abonné et le réveille sans jamais
// bloquer : le producteur ne dépend pas de la vitesse des onglets.
func (b *Bus) Publish(topic Topic) {
	b.mu.Lock()
	subscribers := make([]*Subscription, 0, len(b.subscribers))
	for subscription := range b.subscribers {
		subscribers = append(subscribers, subscription)
	}
	b.mu.Unlock()
	for _, subscription := range subscribers {
		subscription.add(topic)
	}
}

// PublishAtMost publie le sujet au plus une fois par fenêtre. Dans la
// fenêtre, la publication est reportée à sa fin, jamais perdue : le dernier
// changement finit toujours par arriver aux onglets.
func (b *Bus) PublishAtMost(topic Topic, every time.Duration) {
	b.mu.Lock()
	if b.scheduled[topic] {
		b.mu.Unlock()
		return
	}
	wait := every - time.Since(b.lastPublished[topic])
	if wait > 0 {
		b.scheduled[topic] = true
		b.mu.Unlock()
		time.AfterFunc(wait, func() { b.publishScheduled(topic) })
		return
	}
	b.lastPublished[topic] = time.Now()
	b.mu.Unlock()
	b.Publish(topic)
}

func (b *Bus) publishScheduled(topic Topic) {
	b.mu.Lock()
	b.scheduled[topic] = false
	b.lastPublished[topic] = time.Now()
	b.mu.Unlock()
	b.Publish(topic)
}

func (b *Bus) Count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subscribers)
}

// MachineChanged, HeartbeatChanged, ResourcesChanged, ContainersChanged,
// ProbesChanged, StatusChanged, AlertsChanged et DirectoryChanged sont les crochets que les composants
// appellent ; l'identifiant ne sert pas encore : le front relit la liste
// entière. Seules les mesures sont espacées : c'est le seul sujet publié
// en continu par une machine au repos.
func (b *Bus) MachineChanged(string) {
	b.Publish(TopicMachines)
}

func (b *Bus) HeartbeatChanged(string) {
	b.Publish(TopicJobs)
}

func (b *Bus) ResourcesChanged(string) {
	b.PublishAtMost(TopicResources, ResourcesEvery)
}

func (b *Bus) ContainersChanged(string) {
	b.Publish(TopicContainers)
}

func (b *Bus) ProbesChanged(string) {
	b.Publish(TopicProbes)
}

func (b *Bus) StatusChanged() {
	b.Publish(TopicStatus)
}

func (b *Bus) AlertsChanged() {
	b.Publish(TopicAlerts)
}

func (b *Bus) DirectoryChanged() {
	b.Publish(TopicDirectory)
}

func (s *Subscription) add(topic Topic) {
	s.mu.Lock()
	s.pending[topic] = struct{}{}
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Next attend au moins un sujet et rend tout ce qui s'est accumulé, trié,
// puis repart de zéro. Le contexte arrête l'attente.
func (s *Subscription) Next(ctx context.Context) ([]Topic, error) {
	for {
		if topics := s.take(); len(topics) > 0 {
			return topics, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.wake:
		}
	}
}

func (s *Subscription) take() []Topic {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		return nil
	}
	topics := make([]Topic, 0, len(s.pending))
	for topic := range s.pending {
		topics = append(topics, topic)
	}
	clear(s.pending)
	sort.Slice(topics, func(i, j int) bool { return topics[i] < topics[j] })
	return topics
}

// Close désabonne ; les sujets encore en attente sont perdus avec l'onglet.
func (s *Subscription) Close() {
	s.bus.mu.Lock()
	delete(s.bus.subscribers, s)
	s.bus.mu.Unlock()
}
