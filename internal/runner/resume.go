package runner

import (
	"context"
	"errors"

	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/store"
)

// Resume rejoue au démarrage toute action préparée ou en cours : c'est ce que
// le journal de transaction achète (05-execution.md). Rien ne reste dans le
// flou, et une coupure ne perd rien.
func (r *Runner) Resume(ctx context.Context) error {
	pending, err := r.store.PendingActions(ctx)
	if err != nil {
		return err
	}

	for _, action := range pending {
		machine, err := r.store.Machine(ctx, action.MachineID)
		if err != nil {
			r.logger.Error("resume action", "action_id", action.ID, "error", err)
			continue
		}
		if action.State == store.StateRunning {
			// L'unité est partie : on reprend journald après le dernier
			// curseur, on ne relance surtout pas.
			r.submit(machine, job{action: action})
			continue
		}
		r.resumePrepared(ctx, machine, action)
	}
	r.logger.Info("actions resumed", "count", len(pending))
	return nil
}

// resumePrepared refait la préparation depuis les paramètres journalisés. Un
// script différent depuis la coupure est un refus : ce n'est plus l'action que
// l'opérateur a demandée.
func (r *Runner) resumePrepared(ctx context.Context, machine store.Machine, action store.Action) {
	prepared, err := r.catalog.Prepare(catalog.Kind(action.Kind), action.Params)
	var refused refusal.Refusal
	if errors.As(err, &refused) {
		r.conclude(ctx, action, store.StateRefused, nil, "", refused.Error())
		return
	}
	if err != nil {
		r.conclude(ctx, action, store.StateFailed, nil, "", messagePrepareFailed(err))
		return
	}
	if prepared.ScriptDigest != action.ScriptDigest {
		r.conclude(ctx, action, store.StateRefused, nil, "", messageScriptChanged)
		return
	}
	r.submit(machine, job{action: action, prepared: prepared, deposit: true})
}
