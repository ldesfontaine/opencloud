package ratelimit

import (
	"sync"
	"sync/atomic"
)

// Slots compte des places bornées, prises et rendues d'un geste : vérifier
// puis compter en deux temps laisserait deux demandes passer sur la
// dernière place.
type Slots struct {
	limit int32
	taken atomic.Int32
}

func NewSlots(limit int32) *Slots {
	return &Slots{limit: limit}
}

// Take réserve une place ; faux quand elles sont toutes prises.
func (s *Slots) Take() bool {
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

func (s *Slots) Release() {
	s.taken.Add(-1)
}

// Taken dit combien de places sont prises, pour les tests.
func (s *Slots) Taken() int {
	return int(s.taken.Load())
}

// SourceSlots borne les places qu'une même source tient à la fois, en plus du
// plafond global : sans elle, une adresse qui ouvre un flux par seconde finit
// par garder toutes les places du pool, et le limiteur d'ouverture n'y change
// rien. La clé est celle des limiteurs (SourceKey). Une source sans
// place prise n'a pas d'entrée : la table ne garde que les flux en cours.
type SourceSlots struct {
	limit int

	mu    sync.Mutex
	taken map[string]int
}

func NewSourceSlots(limit int) *SourceSlots {
	return &SourceSlots{limit: limit, taken: make(map[string]int)}
}

// Take réserve une place pour cette source ; faux quand elle tient déjà sa part.
func (s *SourceSlots) Take(source string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.taken[source] >= s.limit {
		return false
	}
	s.taken[source]++
	return true
}

func (s *SourceSlots) Release(source string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.taken[source] <= 1 {
		delete(s.taken, source)
		return
	}
	s.taken[source]--
}

// Sources dit combien de sources tiennent une place, pour les tests.
func (s *SourceSlots) Sources() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.taken)
}
