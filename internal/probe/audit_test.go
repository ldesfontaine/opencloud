package probe_test

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
)

// Une sonde dont la cible porte encore un mot de passe, d'avant leur refus,
// est mise en pause au démarrage par une règle : le journal le dit, cible
// masquée.
func TestPauseTargetsWithCredentials_IsJournaledAsARuleWithTheTargetMasked(t *testing.T) {
	b := newBench(t)
	b.service.SetAuditor(audit.New(b.db, slog.New(slog.NewTextHandler(os.Stderr, nil))))
	const password = "mdp-4711" // gitleaks:allow -- faux secret de test
	seeded := storetest.InsertProbe(t, b.db, "5ec0be5ec0be5ec0", probe.Definition{
		Name: "api interne", Kind: probe.KindHTTP, Target: "https://svc:" + password + "@interne.exemple.fr/health", MachineID: machine.LocalID,
	}, testNow)
	ctx := audit.WithActor(context.Background(), audit.Rule(""))
	if err := b.service.PauseTargetsWithCredentials(ctx); err != nil {
		t.Fatal(err)
	}
	events, err := b.db.ListAuditEvents(context.Background(), audit.Query{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events: %+v", events)
	}
	paused := events[0]
	if paused.Action != audit.ProbePause || paused.Actor.Kind != audit.ActorRule || paused.ObjectID != seeded.ID ||
		paused.Details["machine_id"] != machine.LocalID || strings.Contains(paused.Details["target"], password) {
		t.Fatalf("event: %+v", paused)
	}
}
