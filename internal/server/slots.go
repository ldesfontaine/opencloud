package server

import (
	"sync"
	"sync/atomic"
)

// slots compte des places bornées, prises et rendues d'un geste : vérifier
// puis compter en deux temps laisserait deux demandes passer sur la
// dernière place.
type slots struct {
	limit int32
	taken atomic.Int32
}

func newSlots(limit int32) *slots {
	return &slots{limit: limit}
}

// take réserve une place ; faux quand elles sont toutes prises.
func (s *slots) take() bool {
	for {
		taken := s.taken.Load()
		if taken >= s.limit {
			return false
		}
		if s.taken.CompareAndSwap(taken, taken+1) {
			return true
		}
	}
}

func (s *slots) release() {
	s.taken.Add(-1)
}

// sourceSlots borne les places qu'une même source tient à la fois, en plus du
// plafond global : sans elle, une adresse qui ouvre un flux par seconde finit
// par garder toutes les places du pool, et le limiteur d'ouverture n'y change
// rien. La clé est celle des limiteurs (ratelimit.SourceKey). Une source sans
// place prise n'a pas d'entrée : la table ne garde que les flux en cours.
type sourceSlots struct {
	limit int

	mu    sync.Mutex
	taken map[string]int
}

func newSourceSlots(limit int) *sourceSlots {
	return &sourceSlots{limit: limit, taken: make(map[string]int)}
}

// take réserve une place pour cette source ; faux quand elle tient déjà sa part.
func (s *sourceSlots) take(source string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.taken[source] >= s.limit {
		return false
	}
	s.taken[source]++
	return true
}

func (s *sourceSlots) release(source string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.taken[source] <= 1 {
		delete(s.taken, source)
		return
	}
	s.taken[source]--
}
