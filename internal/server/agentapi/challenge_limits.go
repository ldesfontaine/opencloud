package agentapi

import (
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/ratelimit"
)

// Un agent demande un défi par connexion, et revient au plus toutes les
// quelques secondes quand le serveur le coupe : une par seconde, dix d'un
// coup, laisse passer une flotte derrière la même adresse (ou le même /64
// en IPv6, voir ratelimit.SourceKey) qui redémarre : l'agent recule seul,
// d'une à soixante secondes, quand il est refusé.
// Au-delà, c'est quelqu'un qui cherche des identifiants de machine ou qui
// remplit la table des défis. Derrière Traefik sans trusted_proxies, tous
// les agents partagent un seul seau.
const (
	challengePerSourceRate  = 1
	challengePerSourceBurst = 10
	challengeSweepEvery     = 5 * time.Minute
	challengeSweepIdle      = 10 * time.Minute
)

type challengeLimits struct {
	bySource *ratelimit.Limiter
	now      func() time.Time

	mu        sync.Mutex
	lastSweep time.Time
}

func newChallengeLimits() *challengeLimits {
	return &challengeLimits{
		bySource: ratelimit.New(challengePerSourceRate, challengePerSourceBurst),
		now:      time.Now,
	}
}

func (l *challengeLimits) setClock(now func() time.Time) {
	l.now = now
	l.bySource.SetClock(now)
}

// allow compte la requête, et balaie au passage les seaux oubliés.
func (l *challengeLimits) allow(source string) bool {
	now := l.now()
	l.mu.Lock()
	due := now.Sub(l.lastSweep) >= challengeSweepEvery
	if due {
		l.lastSweep = now
	}
	l.mu.Unlock()
	if due {
		l.bySource.Sweep(challengeSweepIdle)
	}
	return l.bySource.Allow(ratelimit.SourceKey(source))
}
