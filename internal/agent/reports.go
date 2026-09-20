package agent

import (
	"sync"

	"github.com/ldesfontaine/opencloud/internal/service"
)

// reports garde ce que le veilleur Docker a observé entre deux signaux :
// le dernier état du démon, le dernier inventaire complet, la dernière
// liste des réseaux, les événements et les mesures dans l'ordre. Plein, il
// oublie le plus ancien.
type reports struct {
	mu      sync.Mutex
	pending service.Report
}

// Deliver fait de reports un Sink pour dockerwatch.
func (r *reports) Deliver(report service.Report) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if report.Engine != nil {
		r.pending.Engine = report.Engine
	}
	if report.Complete {
		r.pending.Complete = true
		r.pending.Inventory = report.Inventory
	}
	if report.NetworksComplete {
		r.pending.NetworksComplete = true
		r.pending.Networks = report.Networks
	}
	r.pending.Events = appendBounded(r.pending.Events, report.Events, service.MaxEventsPerReport)
	r.pending.Stats = appendBounded(r.pending.Stats, report.Stats, service.MaxStatsPerReport)
}

func appendBounded[T any](kept, added []T, limit int) []T {
	kept = append(kept, added...)
	if len(kept) > limit {
		kept = append([]T(nil), kept[len(kept)-limit:]...)
	}
	return kept
}

// take vide le tampon ; nil s'il n'y a rien à dire.
func (r *reports) take() *service.Report {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pending.IsEmpty() {
		return nil
	}
	taken := r.pending
	r.pending = service.Report{}
	return &taken
}

// restore remet devant ce qu'un signal n'a pas pu livrer ; ce qui a été
// observé entre-temps passe derrière.
func (r *reports) restore(report *service.Report) {
	if report == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pending.Engine == nil {
		r.pending.Engine = report.Engine
	}
	if !r.pending.Complete && report.Complete {
		r.pending.Complete, r.pending.Inventory = true, report.Inventory
	}
	if !r.pending.NetworksComplete && report.NetworksComplete {
		r.pending.NetworksComplete, r.pending.Networks = true, report.Networks
	}
	r.pending.Events = appendBounded(report.Events, r.pending.Events, service.MaxEventsPerReport)
	r.pending.Stats = appendBounded(report.Stats, r.pending.Stats, service.MaxStatsPerReport)
}

// markReplayed marque ce qui a attendu une reconnexion : le serveur
// l'écrit sans y voir du neuf.
func (r *reports) markReplayed() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.pending.Events {
		r.pending.Events[i].Replayed = true
	}
}
