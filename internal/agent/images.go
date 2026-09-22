package agent

import (
	"sync"

	"github.com/ldesfontaine/opencloud/internal/update"
)

// images garde ce que la boucle des mises à jour a constaté entre deux
// signaux ; plein, il oublie le plus ancien. Un passage complet tient
// dans un rapport, une coupure en retient quelques-uns.
type images struct {
	mu      sync.Mutex
	pending []update.Result
}

// Deliver fait de images un update.Sink.
func (i *images) Deliver(report update.Report) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.pending = appendBounded(i.pending, report.Results, update.MaxResultsPerReport)
}

// take vide le tampon ; nil s'il n'y a rien à dire.
func (i *images) take() *update.Report {
	i.mu.Lock()
	defer i.mu.Unlock()
	if len(i.pending) == 0 {
		return nil
	}
	taken := update.Report{Results: i.pending}
	i.pending = nil
	return &taken
}

// restore remet devant ce qu'un signal n'a pas pu livrer.
func (i *images) restore(report *update.Report) {
	if report == nil {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.pending = appendBounded(report.Results, i.pending, update.MaxResultsPerReport)
}

// markReplayed marque ce qui a attendu une reconnexion.
func (i *images) markReplayed() {
	i.mu.Lock()
	defer i.mu.Unlock()
	for index := range i.pending {
		i.pending[index].Replayed = true
	}
}
