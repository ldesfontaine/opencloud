package agent

import (
	"sync"

	"github.com/ldesfontaine/opencloud/internal/probe"
)

// Une heure d'essais pour un jeu plein de sondes à la cadence la plus
// serrée : ce qu'une coupure peut retenir. Pas de spool sur disque, un
// redémarrage de l'agent perd au plus ce tampon.
const maxBufferedResults = probe.MaxResultsPerReport

// results garde ce que les sondes ont donné entre deux signaux, dans
// l'ordre ; plein, il oublie le plus ancien.
type results struct {
	mu      sync.Mutex
	pending []probe.Result
}

// Deliver fait de results un probe.Sink.
func (r *results) Deliver(report probe.Report) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending = appendBounded(r.pending, report.Results, maxBufferedResults)
}

// take vide le tampon ; nil s'il n'y a rien à dire.
func (r *results) take() *probe.Report {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.pending) == 0 {
		return nil
	}
	taken := probe.Report{Results: r.pending}
	r.pending = nil
	return &taken
}

// restore remet devant ce qu'un signal n'a pas pu livrer ; ce qui a été
// sondé entre-temps passe derrière.
func (r *results) restore(report *probe.Report) {
	if report == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending = appendBounded(report.Results, r.pending, maxBufferedResults)
}

// markReplayed marque ce qui a attendu une reconnexion : le serveur
// l'écrit dans l'histoire sans y voir du neuf.
func (r *results) markReplayed() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.pending {
		r.pending[i].Replayed = true
	}
}
