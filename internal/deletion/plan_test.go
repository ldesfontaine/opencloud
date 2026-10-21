package deletion_test

import (
	"errors"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/deletion"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

var (
	errHasProbes = refusal.Conflict("thing.has_probes", "the thing runs probes")
	errShown     = refusal.Conflict("thing.shown", "a component shows the thing")
)

func reasons() map[deletion.Kind]error {
	return map[deletion.Kind]error{deletion.KindProbes: errHasProbes, deletion.KindComponents: errShown}
}

// Un genre vide n'entre pas dans le plan : rien à retenir, rien à compter.
func TestPlan_KeepsOnlyKindsThatHoldSomething(t *testing.T) {
	var plan deletion.Plan
	plan.Block(deletion.KindProbes, nil)
	plan.Remove(deletion.KindProbeResults, 0)
	plan.Detach(deletion.KindAlerts, 0)
	if plan.IsBlocked() || len(plan.Removes) != 0 || len(plan.Detaches) != 0 {
		t.Fatalf("plan = %+v", plan)
	}
	if err := deletion.Refuse(plan, reasons()); err != nil {
		t.Fatalf("un plan libre ne refuse pas: %v", err)
	}
}

// Le refus porte la clé du premier bloqueur, et le plan entier.
func TestRefuse_CarriesTheKeyOfTheFirstBlockerAndThePlan(t *testing.T) {
	var plan deletion.Plan
	plan.Block(deletion.KindComponents, []deletion.Object{{ID: "c1", Name: "Nuage"}})
	plan.Block(deletion.KindProbes, []deletion.Object{{ID: "p1", Name: "site"}})

	err := deletion.Refuse(plan, reasons())

	var refused *deletion.RefusedError
	if !errors.As(err, &refused) || len(refused.Plan.Blockers) != 2 {
		t.Fatalf("le refus doit porter le plan: %v", err)
	}
	if key, ok := refusal.KeyOf(err); !ok || key != "thing.shown" || !errors.Is(err, errShown) || !refusal.IsConflict(err) {
		t.Fatalf("clé = %q %v", key, ok)
	}
}

// Un genre qui retient sans refus déclaré est une faute du code : l'erreur
// n'a pas de clé, l'opérateur lira « internal » et le journal la cause.
func TestRefuse_AnUndeclaredBlockerIsAnErrorWithoutKey(t *testing.T) {
	var plan deletion.Plan
	plan.Block(deletion.KindHeartbeats, []deletion.Object{{ID: "h1", Name: "job"}})
	err := deletion.Refuse(plan, reasons())
	if err == nil {
		t.Fatal("un plan bloqué doit refuser")
	}
	if key, ok := refusal.KeyOf(err); ok {
		t.Fatalf("pas de clé attendue, obtenu %q", key)
	}
}
