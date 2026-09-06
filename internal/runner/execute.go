package runner

import (
	"context"
	"errors"
	"path"
	"strconv"
	"time"

	"github.com/ldesfontaine/opencloud/internal/actiondir"
	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/transport"
)

// Les modes des trois fichiers d'une action (15-catalogue-actions.md §1).
const (
	scriptMode  = 0o755
	paramsMode  = 0o600 // lu par systemd, jamais par un shell
	timeoutMode = 0o644
)

func (r *Runner) execute(ctx context.Context, machine store.Machine, current job) {
	action := current.action

	machineTransport, err := r.transports.For(ctx, machine)
	if errors.Is(err, ErrNotEnrolled) {
		r.conclude(ctx, action, store.StateRefused, nil, "", messageNotEnrolled(machine))
		return
	}
	if err != nil {
		r.concludeUnlessStopping(ctx, action, store.StateFailed, "", messageNoTransport(machine, err))
		return
	}

	if current.deposit && !r.depositAndLaunch(ctx, machineTransport, &action, current.prepared) {
		return
	}
	r.follow(ctx, machineTransport, action)
}

// depositAndLaunch pose les fichiers puis lance l'unité. ErrAlreadyLaunched
// n'est pas une erreur : l'action est déjà partie, il n'y a qu'à la suivre.
func (r *Runner) depositAndLaunch(ctx context.Context, machineTransport transport.Transport, action *store.Action, prepared catalog.Prepared) bool {
	if err := r.deposit(ctx, machineTransport, action.ID, prepared); err != nil {
		r.concludeUnlessStopping(ctx, *action, store.StateFailed, "", messageDepositFailed(err))
		return false
	}

	err := machineTransport.Launch(ctx, action.ID)
	if err != nil && !errors.Is(err, transport.ErrAlreadyLaunched) {
		r.concludeUnlessStopping(ctx, *action, store.StateFailed, "", messageLaunchFailed(err))
		return false
	}

	launchedAt := time.Now().UTC()
	if err := r.store.MarkRunning(ctx, action.ID, launchedAt); err != nil {
		// La ligne reste « prepared » : la reprise la retrouvera.
		r.logger.Error("mark action running", "action_id", action.ID, "error", err)
		return false
	}
	action.State = store.StateRunning
	action.LaunchedAt = launchedAt
	r.logger.Info("action launched", "action_id", action.ID, "unit", action.UnitName)
	return true
}

func (r *Runner) deposit(ctx context.Context, machineTransport transport.Transport, actionID string, prepared catalog.Prepared) error {
	if err := machineTransport.Put(ctx, actionID, actiondir.ScriptName, prepared.Script, scriptMode); err != nil {
		return err
	}
	if err := machineTransport.Put(ctx, actionID, actiondir.ParamsName, prepared.ParamsEnv, paramsMode); err != nil {
		return err
	}
	seconds := []byte(strconv.Itoa(timeoutSeconds(prepared.Definition)) + "\n")
	if err := machineTransport.Put(ctx, actionID, actiondir.TimeoutName, seconds, timeoutMode); err != nil {
		return err
	}
	for _, file := range prepared.Files {
		name := path.Join(actiondir.FilesDirName, file.Path)
		if err := machineTransport.Put(ctx, actionID, name, file.Content, file.Mode); err != nil {
			return err
		}
	}
	return nil
}

// concludeUnlessStopping ne conclut rien quand c'est l'arrêt du runner qui a
// interrompu : l'action reste en attente et la reprise s'en occupera.
func (r *Runner) concludeUnlessStopping(ctx context.Context, action store.Action, state store.ActionState, result, note string) {
	if ctx.Err() != nil {
		return
	}
	r.conclude(ctx, action, state, nil, result, note)
}

// conclude ferme la ligne du journal et prévient les abonnés. L'écriture
// survit à l'annulation du suivi : sans elle, l'action resterait en cours
// alors qu'on connaît son issue.
func (r *Runner) conclude(ctx context.Context, action store.Action, state store.ActionState, exitCode *int, result, note string) {
	writeCtx := context.WithoutCancel(ctx)
	if err := r.store.Conclude(writeCtx, action.ID, state, exitCode, result, note); err != nil {
		r.logger.Error("conclude action", "action_id", action.ID, "error", err)
		return
	}
	r.logger.Info("action concluded", "action_id", action.ID, "state", string(state), "note", note)
	r.publish(action.ID, Event{Done: true, State: state, Result: result})
}
