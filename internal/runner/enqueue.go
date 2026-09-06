package runner

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/actiondir"
	"github.com/ldesfontaine/opencloud/internal/catalog"
	"github.com/ldesfontaine/opencloud/internal/store"
)

const (
	// L'identifiant : <kind>-<horodatage court>-<4 caractères aléatoires>.
	// Il doit tenir dans la forme que sudoers et le lanceur revalident.
	idStampLayout  = "060102-150405"
	idSuffixLength = 4
	idAttempts     = 5
)

// Enqueue prépare l'action, la journalise « prepared », puis la dépose dans la
// file de sa machine. Un refus de préparation remonte tel quel : rien n'est
// journalisé, il ne s'est rien passé.
func (r *Runner) Enqueue(ctx context.Context, machineID string, kind catalog.Kind, params map[string]string) (store.Action, error) {
	machine, err := r.store.Machine(ctx, machineID)
	if err != nil {
		return store.Action{}, err
	}

	prepared, err := r.catalog.Prepare(kind, params)
	if err != nil {
		return store.Action{}, err
	}

	id, err := r.newActionID(ctx, kind)
	if err != nil {
		return store.Action{}, err
	}

	action := store.Action{
		ID:             id,
		MachineID:      machine.ID,
		Kind:           string(kind),
		Params:         prepared.Params,
		State:          store.StatePrepared,
		ScriptDigest:   prepared.ScriptDigest,
		UnitName:       actiondir.UnitName(id),
		TimeoutSeconds: timeoutSeconds(prepared.Definition),
		CreatedAt:      time.Now().UTC(),
	}
	if err := r.store.InsertAction(ctx, action); err != nil {
		return store.Action{}, err
	}
	r.logger.Info("action prepared", "action_id", action.ID, "machine", machine.ID, "kind", action.Kind)

	if !r.submit(machine, job{action: action, prepared: prepared, deposit: true}) {
		return action, ErrClosed
	}
	return action, nil
}

// newActionID tire un identifiant libre. rand.Text rend des caractères de
// l'alphabet base32 en majuscules ; en minuscules ils entrent dans [0-9a-z-].
func (r *Runner) newActionID(ctx context.Context, kind catalog.Kind) (string, error) {
	stamp := time.Now().UTC().Format(idStampLayout)
	for attempt := 0; attempt < idAttempts; attempt++ {
		id := fmt.Sprintf("%s-%s-%s", kind, stamp, strings.ToLower(rand.Text()[:idSuffixLength]))
		if !actiondir.ValidID(id) {
			return "", fmt.Errorf("build action id for %s: %q does not match the launcher pattern", kind, id)
		}
		_, err := r.store.Action(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			return id, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("find a free action id for %s", kind)
}

// timeoutSeconds : le délai devient RuntimeMaxSec sur la machine, dans les
// bornes que le lanceur accepte.
func timeoutSeconds(definition catalog.Definition) int {
	seconds := int(definition.Timeout / time.Second)
	if seconds < actiondir.MinTimeoutSeconds {
		return actiondir.MinTimeoutSeconds
	}
	if seconds > actiondir.MaxTimeoutSeconds {
		return actiondir.MaxTimeoutSeconds
	}
	return seconds
}
