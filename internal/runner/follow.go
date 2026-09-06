package runner

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/transport"
)

// follow lit journald jusqu'à la fin de l'unité. Une machine injoignable ne
// conclut rien : on ne sait rien pour l'instant, on réessaie — jusqu'au délai
// maximum de l'action plus une marge, après quoi le suivi est abandonné et
// l'opérateur reçoit la commande pour aller voir.
func (r *Runner) follow(ctx context.Context, machineTransport transport.Transport, action store.Action) {
	output := &outputTracker{runner: r, ctx: ctx, actionID: action.ID, cursor: action.LastCursor}
	deadline := r.abandonDeadline(action)
	var unreachableSince time.Time

	for attempt := 0; ; attempt++ {
		// L'échéance borne aussi une unité qui ne conclut jamais — journal
		// illisible, unité jamais partie — sinon la file de la machine
		// resterait bloquée dessus. RuntimeMaxSec l'a tuée de toute façon.
		followCtx, cancel := context.WithDeadline(ctx, deadline)
		outcome, err := machineTransport.Follow(followCtx, action.ID, output.cursor, output.emit)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			r.concludeFromOutcome(ctx, action, outcome, output.result)
			return
		}
		if !errors.Is(err, transport.ErrUnreachable) {
			if followCtx.Err() != nil {
				r.conclude(ctx, action, store.StateFailed, nil, output.result, messageFollowDeadline(action))
				return
			}
			r.conclude(ctx, action, store.StateFailed, nil, output.result, messageFollowFailed(err))
			return
		}

		if unreachableSince.IsZero() {
			unreachableSince = time.Now()
			r.logger.Warn("machine unreachable while following", "action_id", action.ID)
		}
		if !time.Now().Before(deadline) {
			r.conclude(ctx, action, store.StateFailed, nil, output.result,
				messageFollowAbandoned(action, time.Since(unreachableSince)))
			return
		}
		select {
		case <-time.After(r.retryDelay(attempt)):
		case <-ctx.Done():
			return
		}
	}
}

// abandonDeadline : le délai maximum de l'action, plus une marge, à compter du
// lancement. Au-delà, l'unité est morte de toute façon (RuntimeMaxSec).
func (r *Runner) abandonDeadline(action store.Action) time.Time {
	start := action.LaunchedAt
	if start.IsZero() {
		start = time.Now()
	}
	return start.Add(time.Duration(action.TimeoutSeconds)*time.Second + r.followGrace)
}

func (r *Runner) concludeFromOutcome(ctx context.Context, action store.Action, outcome transport.Outcome, result string) {
	code := outcome.ExitCode
	switch {
	case outcome.TimedOut:
		r.conclude(ctx, action, store.StateFailed, &code, result, messageTimedOut(action))
	case outcome.Killed:
		r.conclude(ctx, action, store.StateFailed, &code, result, messageKilled(action))
	case code == catalog.ExitDone:
		r.conclude(ctx, action, store.StateApplied, &code, result, "")
	case code == catalog.ExitFailed:
		r.conclude(ctx, action, store.StateFailed, &code, result, "")
	case code == catalog.ExitRefused:
		r.conclude(ctx, action, store.StateRefused, &code, result, "")
	default:
		r.conclude(ctx, action, store.StateFailed, &code, result, messageUnexpectedExit(code))
	}
}

// outputTracker stocke chaque ligne, la diffuse, et retient le curseur de
// reprise et le dernier constat vu.
type outputTracker struct {
	runner   *Runner
	ctx      context.Context
	actionID string
	cursor   string
	result   string
}

func (o *outputTracker) emit(line transport.Line) {
	stored, err := o.runner.store.AppendLine(o.ctx, o.actionID, line.At, line.Text, line.Cursor)
	if err != nil {
		o.runner.logger.Error("store action line", "action_id", o.actionID, "error", err)
		return
	}
	o.cursor = line.Cursor
	if constat, found := strings.CutPrefix(strings.TrimSpace(line.Text), catalog.ResultPrefix); found {
		o.result = strings.TrimSpace(constat)
	}
	o.runner.publish(o.actionID, Event{Seq: stored.Seq, At: stored.At, Text: stored.Text})
}
