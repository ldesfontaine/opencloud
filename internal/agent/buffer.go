package agent

import (
	"sync"

	"github.com/ldesfontaine/opencloud/internal/sampler"
)

// Une heure de lectures à 10 s : ce qu'une coupure peut retenir avant que
// les plus anciennes ne se perdent. Pas de spool sur disque : un
// redémarrage de l'agent perd au plus ce tampon.
const maxBuffered = 360

// buffer garde les lectures entre deux signaux, dans l'ordre où elles ont
// été faites ; plein, il oublie la plus ancienne.
type buffer struct {
	mu       sync.Mutex
	readings []sampler.Reading
}

func (b *buffer) push(reading sampler.Reading) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.readings) >= maxBuffered {
		b.readings = b.readings[1:]
	}
	b.readings = append(b.readings, reading)
}

// take retire jusqu'à limit lectures, les plus anciennes d'abord.
func (b *buffer) take(limit int) []sampler.Reading {
	b.mu.Lock()
	defer b.mu.Unlock()
	count := min(limit, len(b.readings))
	taken := make([]sampler.Reading, count)
	copy(taken, b.readings[:count])
	b.readings = append([]sampler.Reading(nil), b.readings[count:]...)
	return taken
}

// restore remet devant ce qu'un signal n'a pas pu livrer.
func (b *buffer) restore(readings []sampler.Reading) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.readings = append(append([]sampler.Reading(nil), readings...), b.readings...)
	if len(b.readings) > maxBuffered {
		b.readings = b.readings[len(b.readings)-maxBuffered:]
	}
}
