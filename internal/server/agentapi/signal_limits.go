package agentapi

import (
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/ratelimit"
)

// Un agent donne un signal toutes les 30 s (agent.DefaultSignalInterval).
// Après une coupure il rattrape son tampon, 360 lectures par lots de 120 :
// trois signaux pleins et un dernier, serrés, soit quatre d'un coup au pire.
// Une rafale de 30 couvre sept rattrapages, et une demi-requête par seconde
// quinze fois la cadence normale : un agent sain ne se voit jamais refuser.
// Au-delà, c'est une machine qui inonde, et chaque signal coûte une
// transaction et jusqu'à un mégaoctet de lignes.
//
// Le seau est celui de la machine, trouvée par sa session : l'adresse ne
// dit rien, des agents derrière un même proxy la partagent.
const (
	signalPerMachineRate  = 0.5
	signalPerMachineBurst = 30
	signalSweepEvery      = 5 * time.Minute
	signalSweepIdle       = 10 * time.Minute
)

type signalLimits struct {
	byMachine *ratelimit.Limiter
	now       func() time.Time

	mu        sync.Mutex
	lastSweep time.Time
}

func newSignalLimits() *signalLimits {
	return &signalLimits{
		byMachine: ratelimit.New(signalPerMachineRate, signalPerMachineBurst),
		now:       time.Now,
	}
}

func (l *signalLimits) setClock(now func() time.Time) {
	l.now = now
	l.byMachine.SetClock(now)
}

// allow compte le signal de cette machine, et balaie au passage les seaux
// des machines retirées.
func (l *signalLimits) allow(machineID string) bool {
	now := l.now()
	l.mu.Lock()
	due := now.Sub(l.lastSweep) >= signalSweepEvery
	if due {
		l.lastSweep = now
	}
	l.mu.Unlock()
	if due {
		l.byMachine.Sweep(signalSweepIdle)
	}
	return l.byMachine.Allow(machineID)
}
