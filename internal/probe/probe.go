package probe

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/transport"
)

// Une note tient dans une ligne de l'écran ; ce qu'une machine raconte est une
// entrée non fiable, sa longueur comprise.
const maxNoteBytes = 500

// Prober est ce que la sonde attend d'une machine : joindre, puis prouver que
// le lanceur répond derrière sudo. Le vrai est *transport.SSH.
type Prober interface {
	Probe(ctx context.Context) error
	CheckLauncher(ctx context.Context) (transport.LauncherReply, error)
}

// Transports rend de quoi sonder une machine. Une erreur dit qu'il n'y a rien
// à sonder — la machine n'est pas enrôlée —, jamais que la machine va mal.
type Transports interface {
	For(ctx context.Context, machine store.Machine) (Prober, error)
}

// Checker sonde les machines, l'une après l'autre. Une machine n'est jamais
// sondée deux fois à la fois : le bouton de la fiche attend la sonde en cours
// plutôt que de la doubler.
type Checker struct {
	store      *store.Store
	transports Transports
	interval   time.Duration
	logger     *slog.Logger
	now        func() time.Time

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func New(database *store.Store, transports Transports, interval time.Duration, logger *slog.Logger) *Checker {
	return &Checker{
		store:      database,
		transports: transports,
		interval:   interval,
		logger:     logger,
		now:        time.Now,
		locks:      map[string]*sync.Mutex{},
	}
}

// Run sonde une première fois, puis à chaque intervalle, jusqu'à l'annulation
// du contexte. Rien n'en sort : le constat va dans la base, pas à l'écran.
func (c *Checker) Run(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		c.sweep(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Now sonde une machine tout de suite : c'est le bouton « Tester l'accès » de
// sa fiche. Il rend ce qui l'a empêché, là où la boucle se contente de le noter.
func (c *Checker) Now(ctx context.Context, machineID string) error {
	machine, err := c.store.Machine(ctx, machineID)
	if err != nil {
		return fmt.Errorf("lire la machine %s : %w", machineID, err)
	}
	return c.probeMachine(ctx, machine)
}

func (c *Checker) sweep(ctx context.Context) {
	machines, err := c.store.Machines(ctx)
	if err != nil {
		c.logger.Error("probe sweep failed", "error", err)
		return
	}

	for _, machine := range machines {
		if ctx.Err() != nil {
			return
		}
		if err := c.probeMachine(ctx, machine); err != nil {
			// Une machine non enrôlée n'a rien à dire, et une erreur de sonde
			// n'arrête pas les autres machines.
			c.logger.Debug("machine not probed", "machine_id", machine.ID, "error", err)
		}
	}
}

func (c *Checker) probeMachine(ctx context.Context, machine store.Machine) error {
	lock := c.lockFor(machine.ID)
	lock.Lock()
	defer lock.Unlock()

	prober, err := c.transports.For(ctx, machine)
	if err != nil {
		return fmt.Errorf("joindre %s : %w", machine.ID, err)
	}

	state, note := observe(ctx, prober)
	if err := c.store.RecordProbe(ctx, machine.ID, c.now(), state, note); err != nil {
		return fmt.Errorf("écrire la remontée de %s : %w", machine.ID, err)
	}
	return nil
}

// observe joue « Tester l'accès » : ssh répond, puis le lanceur refuse derrière
// sudo comme il doit le faire appelé sans identifiant.
func observe(ctx context.Context, prober Prober) (state, note string) {
	if err := prober.Probe(ctx); err != nil {
		return store.ProbeSSHFailed, bound(err.Error())
	}

	reply, err := prober.CheckLauncher(ctx)
	if err != nil {
		return store.ProbeLauncherFailed, bound(err.Error())
	}
	if reply.ExitCode != catalog.ExitRefused {
		return store.ProbeLauncherFailed, bound(fmt.Sprintf(
			"le lanceur appelé sans identifiant répond %d au lieu de %d : %s",
			reply.ExitCode, catalog.ExitRefused, reply.Output))
	}
	return store.ProbeReachable, ""
}

// lockFor rend le verrou d'une machine, créé au premier passage. Il n'y a
// qu'une poignée de machines : rien à ramasser derrière.
func (c *Checker) lockFor(machineID string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()

	lock, found := c.locks[machineID]
	if !found {
		lock = &sync.Mutex{}
		c.locks[machineID] = lock
	}
	return lock
}

func bound(note string) string {
	if len(note) <= maxNoteBytes {
		return note
	}
	return note[:maxNoteBytes] + "… (tronqué)"
}
