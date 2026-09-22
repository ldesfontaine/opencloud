package alert

import (
	"context"
	"errors"

	"github.com/ldesfontaine/opencloud/internal/service"
)

// Ce que le composant service constate après chaque rapport écrit : la
// fiche telle qu'elle est, et les transitions que ce rapport a notées.
// Un conteneur archivé ne compte plus.
func (e *Engine) ServiceChanged(ctx context.Context, found service.Service, transitions []service.Transition) {
	object := Object{Kind: ObjectService, ID: found.ID, Name: found.Name}
	if found.IsArchived() {
		e.Gone(ctx, object)
		return
	}
	e.judgeRestarts(ctx, found, object, transitions)
	e.judgeState(ctx, found, object)
}

// judgeState lit l'état final : arrêté sans qu'on l'ait demandé (un code
// hors 0, 137 et 143, ou mort), ou défaillant (le contrôle de santé
// échoue). Tout le reste résout.
func (e *Engine) judgeState(ctx context.Context, found service.Service, object Object) {
	if isUnexpectedStop(found.State, found.ExitCode) {
		e.Open(ctx, Fact{
			Kind: KindServiceDown, Severity: SeverityDanger, Object: object, MachineID: found.MachineID,
			Details: Details{ExitCode: found.ExitCode},
		})
	} else {
		e.Resolve(ctx, KindServiceDown, object)
	}
	if found.State == service.StateRunning && found.Health == service.HealthUnhealthy {
		e.Open(ctx, Fact{Kind: KindServiceUnhealthy, Severity: SeverityAttention, Object: object, MachineID: found.MachineID})
	} else {
		e.Resolve(ctx, KindServiceUnhealthy, object)
	}
}

func isUnexpectedStop(state service.State, exitCode int) bool {
	if state == service.StateDead {
		return true
	}
	return state == service.StateExited && !service.IsCleanExit(exitCode)
}

// judgeRestarts compte, dans l'ordre du rapport, les retours en marche
// qui suivent un arrêt non demandé : un die à code non propre puis un
// start, ou un passage par « restarting », la politique de redémarrage de
// Docker. Un arrêt non demandé vu dans un rapport précédent a laissé une
// alerte ouverte : le start qui la résout compte aussi. Les transitions
// rejouées sont de l'histoire, pas du neuf.
func (e *Engine) judgeRestarts(ctx context.Context, found service.Service, object Object, transitions []service.Transition) {
	pendingStop, exitCode := e.hasOpen(ctx, KindServiceDown, object)
	restarts := 0
	for _, transition := range transitions {
		if transition.Replayed {
			continue
		}
		switch transition.NewState {
		case service.StateExited, service.StateDead:
			code := 0
			if transition.ExitCode != nil {
				code = *transition.ExitCode
			}
			pendingStop = isUnexpectedStop(transition.NewState, code)
			exitCode = code
		case service.StateRestarting:
			pendingStop = true
		case service.StateRunning:
			if pendingStop {
				restarts++
			}
			pendingStop = false
		}
	}
	if restarts == 0 {
		return
	}
	e.restarted(ctx, found, object, restarts, exitCode)
}

// hasOpen dit si une alerte de ce type est ouverte sur l'objet, et le
// code de sortie qu'elle porte.
func (e *Engine) hasOpen(ctx context.Context, kind Kind, object Object) (bool, int) {
	existing, err := e.store.GetOpenAlert(ctx, kind, object.Kind, object.ID)
	if errors.Is(err, ErrNotFound) {
		return false, 0
	}
	if err != nil {
		e.fail(ctx, "read open alert", err)
		return false, 0
	}
	return true, existing.Details.ExitCode
}

// restarted ajoute ces redémarrages à ceux de la fenêtre : au troisième,
// c'est une boucle et l'alerte s'aggrave. Un compte plus vieux que la
// fenêtre repart de zéro.
func (e *Engine) restarted(ctx context.Context, found service.Service, object Object, restarts, exitCode int) {
	count := restarts
	existing, err := e.store.GetOpenAlert(ctx, KindServiceRestart, object.Kind, object.ID)
	if err == nil && existing.UpdatedAt.After(e.now().Add(-RestartWindow)) {
		count += existing.Details.Count
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		e.fail(ctx, "read open alert", err)
		return
	}
	severity := SeverityAttention
	if count >= RestartLoopThreshold {
		severity = SeverityDanger
	}
	e.Open(ctx, Fact{
		Kind: KindServiceRestart, Severity: severity, Object: object, MachineID: found.MachineID,
		Details: Details{Count: count, ExitCode: exitCode},
	})
}
