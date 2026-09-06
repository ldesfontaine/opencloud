package runner

import (
	"sync"

	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/store"
)

// job est ce qu'un exécuteur prend dans la file. deposit est faux à la reprise
// d'une action déjà lancée : l'unité tourne, il n'y a qu'à suivre.
type job struct {
	action   store.Action
	prepared catalog.Prepared
	deposit  bool
}

// machineQueue est la file d'une machine. Un seul exécuteur la lit, donc deux
// actions ne peuvent jamais se gêner sur la même machine : c'est structurel,
// il n'y a aucun verrou à écrire (05-execution.md).
type machineQueue struct {
	machine store.Machine
	wake    chan struct{}

	mu   sync.Mutex
	jobs []job
}

func (q *machineQueue) add(next job) {
	q.mu.Lock()
	q.jobs = append(q.jobs, next)
	q.mu.Unlock()

	// Un seul réveil en attente suffit : l'exécuteur vide la file avant de
	// se rendormir.
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *machineQueue) take() (job, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.jobs) == 0 {
		return job{}, false
	}
	next := q.jobs[0]
	q.jobs = q.jobs[1:]
	return next, true
}

// submit dépose dans la file de la machine et démarre son exécuteur la
// première fois. Il rend faux quand le runner s'arrête.
func (r *Runner) submit(machine store.Machine, next job) bool {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return false
	}
	queue, found := r.queues[machine.ID]
	if !found {
		queue = &machineQueue{machine: machine, wake: make(chan struct{}, 1)}
		r.queues[machine.ID] = queue
		r.workers.Add(1)
		go r.serveQueue(queue)
	}
	r.mu.Unlock()

	queue.add(next)
	return true
}

func (r *Runner) serveQueue(queue *machineQueue) {
	defer r.workers.Done()
	for {
		next, found := queue.take()
		if !found {
			select {
			case <-queue.wake:
				continue
			case <-r.lifetime.Done():
				return
			}
		}
		r.execute(r.lifetime, queue.machine, next)
		if r.lifetime.Err() != nil {
			return
		}
	}
}
