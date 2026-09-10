package runner

import (
	"context"
	"errors"
	"fmt"
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

	if current.deposit && !r.depositAndLaunchWithRetries(ctx, machineTransport, &action, current.prepared) {
		return
	}
	r.follow(ctx, machineTransport, action)
}

// depositAndLaunchWithRetries insiste tant que la machine est injoignable —
// une coupure d'openCloud tue aussi le ssh du dépôt, et une machine qui ne
// répond pas maintenant répondra peut-être dans une minute. Rien n'est conclu
// tant que rien n'est parti ; au-delà de l'échéance, on le dit.
func (r *Runner) depositAndLaunchWithRetries(ctx context.Context, machineTransport transport.Transport, action *store.Action, prepared catalog.Prepared) bool {
	deadline := r.abandonDeadline(*action)
	for attempt := 0; ; attempt++ {
		err := r.depositAndLaunch(ctx, machineTransport, action, prepared)
		if ctx.Err() != nil {
			return false
		}
		if err == nil {
			return true
		}
		if !errors.Is(err, transport.ErrUnreachable) {
			r.conclude(ctx, *action, store.StateFailed, nil, "", err.Error())
			return false
		}
		if !time.Now().Before(deadline) {
			r.conclude(ctx, *action, store.StateFailed, nil, "", messageDepositAbandoned(err))
			return false
		}
		r.logger.Warn("machine unreachable while depositing", "action_id", action.ID, "error", err)
		select {
		case <-time.After(r.retryDelay(attempt)):
		case <-ctx.Done():
			return false
		}
	}
}

// depositAndLaunch pose les fichiers puis lance l'unité. ErrAlreadyLaunched
// n'est pas une erreur : l'action est déjà partie, il n'y a qu'à la suivre.
// Une machine injoignable remonte telle quelle, l'appelant réessaie ; le reste
// est un message pour le journal.
func (r *Runner) depositAndLaunch(ctx context.Context, machineTransport transport.Transport, action *store.Action, prepared catalog.Prepared) error {
	if err := r.deposit(ctx, machineTransport, action.ID, prepared); err != nil {
		if errors.Is(err, transport.ErrUnreachable) {
			return err
		}
		return errors.New(messageDepositFailed(err))
	}

	purgedDirectories, err := machineTransport.Launch(ctx, action.ID)
	if err != nil && !errors.Is(err, transport.ErrAlreadyLaunched) {
		if errors.Is(err, transport.ErrUnreachable) {
			return err
		}
		return errors.New(messageLaunchFailed(err))
	}

	launchedAt := time.Now().UTC()
	if err := r.store.MarkRunning(ctx, action.ID, launchedAt); err != nil {
		// La ligne reste « prepared » : la reprise la retrouvera.
		return fmt.Errorf("mark action running: %w", err)
	}
	action.State = store.StateRunning
	action.LaunchedAt = launchedAt
	if purgedDirectories > 0 {
		r.notePurgedDirectories(ctx, *action, purgedDirectories)
	}
	r.logger.Info("action launched", "action_id", action.ID, "unit", action.UnitName)
	return nil
}

// notePurgedDirectories pose dans le journal de l'action ce que le lanceur a
// purgé au passage : une ligne, et seulement s'il y en a eu. Le curseur ne
// bouge pas — cette ligne ne vient pas de journald.
func (r *Runner) notePurgedDirectories(ctx context.Context, action store.Action, purged int) {
	line, err := r.store.AppendLine(ctx, action.ID, time.Now().UTC(), messagePurgedDirectories(purged), action.LastCursor)
	if err != nil {
		r.logger.Error("store purge line", "action_id", action.ID, "error", err)
		return
	}
	r.publish(action.ID, Event{Seq: line.Seq, At: line.At, Text: line.Text})
}

// notifyObserver passe la ligne conclue à qui la suit — la table des hôtes
// virtuels, aujourd'hui. Ce que l'observateur en fait ne regarde pas le runner,
// et ce qu'il rate ne change rien à la conclusion déjà écrite.
func (r *Runner) notifyObserver(ctx context.Context, action store.Action, state store.ActionState, exitCode *int, result string) {
	if r.observer == nil {
		return
	}
	concluded := action
	concluded.State = state
	concluded.ExitCode = exitCode
	concluded.Result = result
	r.observer.ActionConcluded(ctx, concluded)
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
	r.notifyObserver(writeCtx, action, state, exitCode, result)
	r.publish(action.ID, Event{Done: true, State: state, Result: result})
}
