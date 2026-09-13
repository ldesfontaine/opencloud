package live

import (
	"context"
	"sort"
	"sync"
)

// Topic nomme ce qui a changé ; le front relit la ressource qui va avec.
type Topic string

const (
	TopicMachines Topic = "machines"
	TopicJobs     Topic = "jobs"
)

type Bus struct {
	mu          sync.Mutex
	subscribers map[*Subscription]struct{}
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
	return &Bus{subscribers: make(map[*Subscription]struct{})}
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

func (b *Bus) Count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subscribers)
}

// MachineChanged et HeartbeatChanged sont les crochets que les composants
// machine et heartbeat appellent ; l'identifiant ne sert pas encore : le
// front relit la liste entière.
func (b *Bus) MachineChanged(string) {
	b.Publish(TopicMachines)
}

func (b *Bus) HeartbeatChanged(string) {
	b.Publish(TopicJobs)
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
