package ratelimit

import (
	"sync"
	"time"
)

// Un seau par clé : il se remplit au rythme donné jusqu'à sa contenance, et
// chaque requête en retire une unité.
type bucket struct {
	tokens   float64
	lastSeen time.Time
}

type Limiter struct {
	// Unités rendues par seconde, et contenance du seau.
	rate  float64
	burst int
	now   func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

// New borne à `rate` requêtes par seconde en régime continu, avec une
// rafale de `burst` requêtes d'un coup.
func New(rate float64, burst int) *Limiter {
	return &Limiter{rate: rate, burst: burst, now: time.Now, buckets: make(map[string]*bucket)}
}

func (l *Limiter) SetClock(now func() time.Time) {
	l.now = now
}

// Allow dit si la requête de cette clé passe, et la compte si oui.
func (l *Limiter) Allow(key string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	current, ok := l.buckets[key]
	if !ok {
		current = &bucket{tokens: float64(l.burst), lastSeen: now}
		l.buckets[key] = current
	}
	current.tokens = min(float64(l.burst), current.tokens+now.Sub(current.lastSeen).Seconds()*l.rate)
	current.lastSeen = now
	if current.tokens < 1 {
		return false
	}
	current.tokens--
	return true
}

// Sweep oublie les clés silencieuses depuis plus longtemps que `idle` : un
// seau plein n'a rien à mémoriser.
func (l *Limiter) Sweep(idle time.Duration) {
	cutoff := l.now().Add(-idle)
	l.mu.Lock()
	defer l.mu.Unlock()
	for key, current := range l.buckets {
		if current.lastSeen.Before(cutoff) {
			delete(l.buckets, key)
		}
	}
}

// Size sert aux tests et au journal.
func (l *Limiter) Size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}
