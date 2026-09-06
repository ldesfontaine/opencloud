package runner

import (
	"time"

	"github.com/ldesfontaine/opencloud/internal/store"
)

// Event est ce que le direct transporte : une ligne de sortie, ou la fin de
// l'action. Seq ordonne les lignes, et permet au navigateur de reprendre
// après une coupure sans doublon ni trou.
type Event struct {
	Done   bool
	Seq    int64
	At     time.Time
	Text   string
	State  store.ActionState
	Result string
}

type subscriber struct {
	events chan Event
	closed bool
}

// close ne se joue que sous le verrou du runner.
func (s *subscriber) close() {
	if s.closed {
		return
	}
	close(s.events)
	s.closed = true
}

// Subscribe ouvre un flux sur une action. L'appelant rejoue d'abord les lignes
// stockées, puis lit le canal en ignorant les seq déjà vus. La fonction rendue
// se désabonne ; le canal se ferme aussi à la fin de l'action.
func (r *Runner) Subscribe(actionID string) (<-chan Event, func()) {
	current := &subscriber{events: make(chan Event, subscriberBuffer)}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		current.close()
		return current.events, func() {}
	}
	if r.subscribers[actionID] == nil {
		r.subscribers[actionID] = map[*subscriber]struct{}{}
	}
	r.subscribers[actionID][current] = struct{}{}

	return current.events, func() { r.unsubscribe(actionID, current) }
}

func (r *Runner) unsubscribe(actionID string, current *subscriber) {
	r.mu.Lock()
	defer r.mu.Unlock()
	current.close()
	delete(r.subscribers[actionID], current)
	if len(r.subscribers[actionID]) == 0 {
		delete(r.subscribers, actionID)
	}
}

func (r *Runner) publish(actionID string, event Event) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for current := range r.subscribers[actionID] {
		select {
		case current.events <- event:
		default:
			// Abonné trop lent : mieux vaut fermer son flux que lui livrer une
			// sortie trouée. Il se reconnecte et rejoue depuis le stockage.
			current.close()
			delete(r.subscribers[actionID], current)
		}
	}

	if !event.Done {
		return
	}
	for current := range r.subscribers[actionID] {
		current.close()
	}
	delete(r.subscribers, actionID)
}
