package server

import "sync/atomic"

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
