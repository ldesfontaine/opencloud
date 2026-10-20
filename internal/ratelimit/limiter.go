package ratelimit

import (
	"sync"
	"time"
)

// sweepEvery : le balayage des seaux oubliés se fait au passage d'une
// requête, au plus une fois par période, sans goroutine.
const sweepEvery = 5 * time.Minute

// Un seau par clé : il se remplit au rythme donné jusqu'à sa contenance, et
// chaque requête en retire une unité.
type bucket struct {
	tokens   float64
	lastSeen time.Time
}

// Limiter compte par clé brute : une machine, l'empreinte d'un jeton. Pour
// une adresse cliente, c'est BySource.
type Limiter struct {
	// Unités rendues par seconde, et contenance du seau.
	rate  float64
	burst int
	// Un seau silencieux depuis idle est oublié : plein, il n'a rien à
	// mémoriser. idle doit dépasser le temps de remplir le seau.
	idle time.Duration
	now  func() time.Time

	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time
}

// New borne à `rate` requêtes par seconde en régime continu, avec une
// rafale de `burst` requêtes d'un coup ; un seau sans requête depuis `idle`
// est oublié.
func New(rate float64, burst int, idle time.Duration) *Limiter {
	return &Limiter{rate: rate, burst: burst, idle: idle, now: time.Now, buckets: make(map[string]*bucket)}
}

func (l *Limiter) SetClock(now func() time.Time) {
	l.now = now
}

// Allow dit si la requête de cette clé passe, et la compte si oui. Les
// seaux oubliés sont balayés au passage.
func (l *Limiter) Allow(key string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweepIfDue(now)
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

// sweepIfDue se tient sous le verrou de Allow.
func (l *Limiter) sweepIfDue(now time.Time) {
	if now.Sub(l.lastSweep) < sweepEvery {
		return
	}
	l.lastSweep = now
	cutoff := now.Add(-l.idle)
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

// BySource compte par adresse cliente, normalisée par sourceKey : l'appelant
// passe l'adresse telle qu'il la lit, il ne peut pas oublier le /64.
type BySource struct {
	limiter *Limiter
}

// NewBySource : les mêmes bornes que New, par source.
func NewBySource(rate float64, burst int, idle time.Duration) *BySource {
	return &BySource{limiter: New(rate, burst, idle)}
}

func (b *BySource) SetClock(now func() time.Time) {
	b.limiter.SetClock(now)
}

// Allow dit si la requête de cette adresse passe, et la compte si oui.
func (b *BySource) Allow(address string) bool {
	return b.limiter.Allow(sourceKey(address))
}
