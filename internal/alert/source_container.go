package alert

import (
	"context"
	"errors"

	"github.com/ldesfontaine/opencloud/internal/container"
)

// Ce que le composant conteneur constate après chaque rapport écrit : la
// fiche telle qu'elle est, et les transitions que ce rapport a notées.
// Un conteneur archivé ne compte plus.
func (e *Engine) ContainerChanged(ctx context.Context, found container.Container, transitions []container.Transition) {
	object := Object{Kind: ObjectService, ID: found.ID, Name: found.Name}
	if found.IsArchived() {
		e.Gone(ctx, object)
		return
	}
	e.judgeCrash(ctx, found, object, transitions)
	e.judgeHealth(ctx, found, object)
}

// judgeHealth ouvre « défaillant » quand le contrôle de santé échoue. Revenu
// bon, l'état n'est noté que comme calme : le balayage résout passé
// ServiceClearDelay, et un échec qui revient avant reprend la même alerte.
func (e *Engine) judgeHealth(ctx context.Context, found container.Container, object Object) {
	if found.State == container.StateRunning && found.Health == container.HealthUnhealthy {
		e.Open(ctx, Fact{Kind: KindServiceUnhealthy, Severity: SeverityAttention, Object: object, MachineID: found.MachineID})
		return
	}
	e.calm(ctx, KindServiceUnhealthy, object)
}

// calm note que l'état est redevenu bon, s'il ne l'était pas déjà.
func (e *Engine) calm(ctx context.Context, kind Kind, object Object) {
	e.mu.Lock()
	defer e.mu.Unlock()
	existing, err := e.store.GetOpenAlert(ctx, kind, object.Kind, object.ID)
	if errors.Is(err, ErrNotFound) {
		return
	}
	if err != nil {
		e.fail(ctx, "read open alert", err)
		return
	}
	if !existing.ClearSince.IsZero() {
		return
	}
	fact := Fact{Kind: kind, Severity: existing.Severity, Object: object, MachineID: existing.MachineID, Details: existing.Details, ClearSince: e.now()}
	e.update(ctx, existing, fact)
}

// judgeCrash tient l'unique alerte vivante d'un conteneur qui plante. Arrêté
// sans qu'on l'ait demandé (un code hors 0, 137 et 143, tué faute de
// mémoire ou sans arrêt demandé, ou mort), elle dit « arrêté » ; relancé,
// elle devient « redémarrage non demandé » et compte les redémarrages de
// la fenêtre ; au troisième, c'est une boucle, et la même alerte s'aggrave.
// Elle ne s'ouvre et ne se résout pas à chaque tour : le balayage la résout
// après un calme de RestartWindow une fois relancé, de ServiceClearDelay
// sinon.
//
// Les redémarrages sont les retours en marche qui suivent un arrêt non
// demandé : un die à code non propre puis un start, ou un passage par
// « restarting », la politique de redémarrage de Docker. Un arrêt vu dans
// un rapport précédent compte aussi, son alerte le dit. Les transitions
// rejouées sont de l'histoire, pas du neuf.
func (e *Engine) judgeCrash(ctx context.Context, found container.Container, object Object, transitions []container.Transition) {
	e.mu.Lock()
	defer e.mu.Unlock()
	existing, alive := e.openCrash(ctx, object)
	pendingStop := alive && existing.Kind == KindServiceDown && existing.ClearSince.IsZero()
	details := Details{}
	if alive {
		details = existing.Details
	}
	restarts := 0
	for _, transition := range transitions {
		if transition.Replayed {
			continue
		}
		switch transition.NewState {
		case container.StateExited, container.StateDead:
			code := 0
			if transition.ExitCode != nil {
				code = *transition.ExitCode
			}
			pendingStop = isUnexpectedStop(transition.NewState, code, transition.OOMKilled, transition.Killed)
			if pendingStop {
				details.ExitCode = code
				details.Reason = reasonOf(transition.OOMKilled, transition.Killed)
			}
		case container.StateRestarting:
			pendingStop = true
		case container.StateRunning:
			if pendingStop {
				restarts++
			}
			pendingStop = false
		}
	}
	down := isUnexpectedStop(found.State, found.ExitCode, found.OOMKilled, found.Killed)
	if !alive && !down && restarts == 0 {
		return
	}
	now := e.now()
	// Un compte plus vieux que la fenêtre repart de zéro.
	if !alive || !existing.UpdatedAt.After(now.Add(-RestartWindow)) {
		details.Count = 0
	}
	details.Count += restarts
	fact := Fact{Kind: KindServiceRestart, Severity: SeverityAttention, Object: object, MachineID: found.MachineID, Details: details}
	switch {
	case down:
		fact.Kind = KindServiceDown
		fact.Severity = SeverityDanger
		fact.Details.ExitCode = found.ExitCode
		fact.Details.Reason = reasonOf(found.OOMKilled, found.Killed)
	case restarts > 0:
		fact.ClearSince = now
	default:
		// Revenu bon sans redémarrage compté : le même type, le calme court.
		fact.Kind = existing.Kind
		fact.ClearSince = existing.ClearSince
		if fact.ClearSince.IsZero() {
			fact.ClearSince = now
		}
	}
	if fact.Details.Count >= RestartLoopThreshold {
		fact.Severity = SeverityDanger
	}
	if !alive {
		e.open(ctx, fact)
		return
	}
	e.update(ctx, existing, fact)
}

// openCrash rend l'alerte vivante de plantage du conteneur. Une base d'avant
// l'anti-rebond peut en porter deux, arrêt et redémarrage : la plus
// récente reste, l'autre est résolue.
func (e *Engine) openCrash(ctx context.Context, object Object) (Alert, bool) {
	var alive []Alert
	for _, kind := range []Kind{KindServiceDown, KindServiceRestart} {
		existing, err := e.store.GetOpenAlert(ctx, kind, object.Kind, object.ID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			e.fail(ctx, "read open alert", err)
			continue
		}
		alive = append(alive, existing)
	}
	switch len(alive) {
	case 0:
		return Alert{}, false
	case 1:
		return alive[0], true
	}
	newest, older := alive[0], alive[1]
	if older.UpdatedAt.After(newest.UpdatedAt) {
		newest, older = older, newest
	}
	e.resolve(ctx, older)
	return newest, true
}

func reasonOf(oomKilled, killed bool) string {
	switch {
	case oomKilled:
		return ReasonOOM
	case killed:
		return ReasonKilled
	}
	return ""
}

// Les motifs d'un conteneur arrêté sans qu'on l'ait demandé : faute de
// mémoire, ou tué en SIGKILL sans demande d'arrêt.
const (
	ReasonOOM    = "oom"
	ReasonKilled = "killed"
)

// ServiceDownReasons liste les motifs d'un arrêt non demandé qui ont leur
// propre phrase ; sans motif, c'est le code de sortie qui parle.
func ServiceDownReasons() []string {
	return []string{ReasonOOM, ReasonKilled}
}

func isUnexpectedStop(state container.State, exitCode int, oomKilled, killed bool) bool {
	if state == container.StateDead {
		return true
	}
	return state == container.StateExited && !container.IsCleanStop(exitCode, oomKilled, killed)
}
