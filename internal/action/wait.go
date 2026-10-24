package action

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// waitSafetyNet borne l'attente d'un réveil : tout changement de run réveille
// Wait, la relecture périodique ne couvre que ce qu'un changement aurait
// laissé passer.
const waitSafetyNet = 30 * time.Second

// changeSignal réveille tous ceux qui attendent le prochain changement de run.
// Un canal que fire ferme, puis remplace : celui qui l'a pris avant de relire
// la base ne manque aucun changement qui suit.
type changeSignal struct {
	mu   sync.Mutex
	next chan struct{}
}

func (c *changeSignal) wait() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.next == nil {
		c.next = make(chan struct{})
	}
	return c.next
}

func (c *changeSignal) fire() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.next != nil {
		close(c.next)
		c.next = nil
	}
}

// Wait attend que le run soit fini (réussi, échoué, refusé ou expiré) et le
// rend avec sa sortie. Il ne borne pas l'attente : le contexte de l'appelant
// le fait ; un run finit de lui-même, au plus OrderLifetime + MaxRunDuration
// après sa demande (Expire et Timeout). Une erreur si le run n'existe plus.
func (s *Service) Wait(ctx context.Context, runID string) (Run, error) {
	for {
		// Pris avant la relecture : un changement entre les deux réveille.
		changed := s.changes.wait()
		run, err := s.store.GetActionRun(ctx, runID)
		if err != nil {
			return Run{}, fmt.Errorf("wait for run: %w", err)
		}
		if run.State.IsFinished() {
			return run, nil
		}
		safetyNet := time.NewTimer(waitSafetyNet)
		select {
		case <-ctx.Done():
			safetyNet.Stop()
			return Run{}, fmt.Errorf("wait for run: %w", ctx.Err())
		case <-changed:
		case <-safetyNet.C:
		}
		safetyNet.Stop()
	}
}
