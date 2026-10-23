package probe

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Sink reçoit ce qu'une machine a sondé : le tampon de l'agent, qui le
// livre avec le signal.
type Sink interface {
	Deliver(Report)
}

// Runner exécute les sondes d'une machine : une goroutine par sonde, sur
// son propre intervalle. Le serveur lui pousse le jeu complet ; une sonde
// inchangée garde sa goroutine, et donc son horloge.
type Runner struct {
	sink    Sink
	checker Checker
	logger  *slog.Logger

	mu      sync.Mutex
	ctx     context.Context
	wanted  []Task
	running map[string]*probeLoop
	loops   sync.WaitGroup
}

type probeLoop struct {
	task   Task
	cancel context.CancelFunc
}

func NewRunner(sink Sink, checker Checker, logger *slog.Logger) *Runner {
	return &Runner{sink: sink, checker: checker, logger: logger, running: make(map[string]*probeLoop)}
}

// Run tient les sondes jusqu'à ce que le contexte s'arrête, puis attend
// que les essais en cours rendent la main.
func (r *Runner) Run(ctx context.Context) {
	r.mu.Lock()
	r.ctx = ctx
	wanted := r.wanted
	r.mu.Unlock()
	r.apply(wanted)
	<-ctx.Done()
	r.stopAll()
	r.loops.Wait()
}

// Assign remplace le jeu de sondes : ce qui n'y est plus s'arrête, ce qui
// a changé repart, ce qui est identique continue. Un jeu reçu avant le
// démarrage attend celui-ci. Le jeu se nettoie d'abord (Sanitize) ; Assign
// rend le nombre de tâches écartées, dites une à une dans le journal et,
// quand elles ont un identifiant que le serveur reconnaîtra, rapportées
// comme un essai « refusée par l'agent » : l'opérateur le lit sur la sonde.
func (r *Runner) Assign(assignment Assignment) int {
	kept, dropped := Sanitize(assignment.Probes)
	r.logDropped(dropped)
	if refused := refusedResults(kept, dropped, time.Now()); len(refused) > 0 {
		r.sink.Deliver(Report{Results: refused})
	}
	r.mu.Lock()
	r.wanted = kept
	started := r.ctx != nil
	r.mu.Unlock()
	if started {
		r.apply(kept)
	}
	return len(dropped)
}

// maxDroppedLogged borne le journal : un serveur qui pousse mille tâches
// fausses ne remplit pas le disque de l'agent.
const maxDroppedLogged = 8

func (r *Runner) logDropped(dropped []Dropped) {
	for _, task := range dropped[:min(len(dropped), maxDroppedLogged)] {
		r.logger.Warn("probe refused by the agent", "probe_id", task.ID, "kind", string(task.Kind),
			"host", task.Host, "reason", task.Reason)
	}
	if more := len(dropped) - maxDroppedLogged; more > 0 {
		r.logger.Warn("probes refused by the agent", "more", more)
	}
}

// Count rend le nombre de sondes en cours, pour les tests et le journal.
func (r *Runner) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.running)
}

func (r *Runner) apply(tasks []Task) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ctx == nil {
		return
	}
	keep := make(map[string]bool, len(tasks))
	for _, task := range tasks {
		keep[task.ID] = true
		if current, ok := r.running[task.ID]; ok {
			if current.task == task {
				continue
			}
			current.cancel()
		}
		ctx, cancel := context.WithCancel(r.ctx)
		r.running[task.ID] = &probeLoop{task: task, cancel: cancel}
		r.loops.Add(1)
		go r.loop(ctx, task)
	}
	for id, current := range r.running {
		if !keep[id] {
			current.cancel()
			delete(r.running, id)
		}
	}
}

func (r *Runner) stopAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, current := range r.running {
		current.cancel()
		delete(r.running, id)
	}
}

// Une sonde part tout de suite, puis suit son intervalle : une sonde qu'on
// vient de créer dit ce qu'elle voit sans faire attendre l'opérateur.
func (r *Runner) loop(ctx context.Context, task Task) {
	defer r.loops.Done()
	r.once(ctx, task)
	ticker := time.NewTicker(task.interval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.once(ctx, task)
		}
	}
}

// once sonde et livre. Un essai interrompu par l'arrêt ne se livre pas :
// ce n'est pas la cible qui n'a pas répondu, c'est nous qui sommes partis.
func (r *Runner) once(ctx context.Context, task Task) {
	result := r.checker.Check(ctx, task, time.Now())
	if ctx.Err() != nil {
		return
	}
	r.logger.Debug("probe checked", "probe_id", task.ID, "target", RedactTarget(task.Target),
		"outcome", string(result.Outcome), "duration_ms", result.DurationMs)
	r.sink.Deliver(Report{Results: []Result{result}})
}
