package machine_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/deletion"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/status"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
)

const remoteID = "11111111-2222-4333-8444-555555555555"

// Le plan d'une machine nomme tout ce qui la retient : les sondes qu'elle
// lance, les tâches qui y sont rattachées, les composants qui l'affichent.
// Le retrait est refusé avec ce plan, puis passe quand l'opérateur a tout
// détaché, et rien de ce qui était déclaré ne part avec elle.
func TestRemove_IsHeldBackByItsPlanThenPassesOnceDetached(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	db := storetest.Open(t)
	machines := machine.New(db, machine.NewSessions(), logger)
	storetest.EnsureLocalMachine(t, machines)
	remote := storetest.EnrollMachine(t, machines, remoteID, "vps-paris-1")
	probes := probe.New(db, logger)
	jobs := heartbeat.New(db, logger)
	pages := status.New(db, db, machines, logger)

	watched, err := probes.Create(ctx, probe.Definition{Name: "site", Kind: probe.KindHTTP, Target: "https://a.fr/", MachineID: remote.ID})
	if err != nil {
		t.Fatal(err)
	}
	job, err := jobs.Create(ctx, heartbeat.Definition{Name: "sauvegarde", MachineID: remote.ID, Interval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	component, err := pages.CreateComponent(ctx, status.ComponentDefinition{PageID: storetest.StatusPage(t, db, "statut"),
		Name:    "Nuage",
		Members: []status.MemberRef{{Kind: status.KindMachine, ID: remote.ID}, {Kind: status.KindProbe, ID: watched.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}

	plan, err := machines.DeletePlan(ctx, remote.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []deletion.Blocker{
		{Kind: deletion.KindProbes, Objects: []deletion.Object{{ID: watched.ID, Name: "site"}}},
		{Kind: deletion.KindHeartbeats, Objects: []deletion.Object{{ID: job.ID, Name: "sauvegarde"}}},
		{Kind: deletion.KindComponents, Objects: []deletion.Object{{ID: component.ID, Name: "Nuage"}}},
	}
	if !reflect.DeepEqual(plan.Blockers, want) {
		t.Fatalf("bloqueurs = %+v", plan.Blockers)
	}

	err = machines.Remove(ctx, remote.ID, machine.RemoveOptions{})
	var refused *deletion.RefusedError
	if !errors.Is(err, machine.ErrHasProbes) || !errors.As(err, &refused) || !reflect.DeepEqual(refused.Plan, plan) {
		t.Fatalf("attendu un refus machine.has_probes avec le plan, obtenu %v", err)
	}
	if _, err := db.GetMachine(ctx, remote.ID); err != nil {
		t.Fatalf("la machine a disparu malgré le refus: %v", err)
	}

	// Détacher : la sonde change de machine, la tâche n'en a plus, le
	// composant ne montre plus la machine (il garde la sonde).
	moved := watched.Definition()
	moved.MachineID = machine.LocalID
	if _, err := probes.Update(ctx, watched.ID, moved); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.Update(ctx, job.ID, heartbeat.Definition{Name: "sauvegarde", Interval: time.Hour}); err != nil {
		t.Fatal(err)
	}
	if _, err := pages.UpdateComponent(ctx, component.ID, status.ComponentDefinition{
		Name: "Nuage", Members: []status.MemberRef{{Kind: status.KindProbe, ID: watched.ID}},
	}); err != nil {
		t.Fatal(err)
	}
	if plan, err := machines.DeletePlan(ctx, remote.ID); err != nil || plan.IsBlocked() {
		t.Fatalf("détachée, la machine ne doit plus être retenue: %+v %v", plan, err)
	}

	if err := machines.Remove(ctx, remote.ID, machine.RemoveOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetProbe(ctx, watched.ID); err != nil {
		t.Fatalf("la sonde déplacée doit rester: %v", err)
	}
	if kept, err := db.GetHeartbeat(ctx, job.ID); err != nil || kept.MachineID != "" {
		t.Fatalf("la tâche détachée doit rester: %+v %v", kept, err)
	}
	if _, err := machines.DeletePlan(ctx, remote.ID); !errors.Is(err, machine.ErrNotFound) {
		t.Fatalf("plan d'une machine retirée: %v", err)
	}
}

// La machine openCloud se retire comme les autres, par son plan : elle
// emporte son agent.
func TestRemove_TheOpenCloudMachineGoesLikeAnyOther(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	db := storetest.Open(t)
	machines := machine.New(db, machine.NewSessions(), logger)
	storetest.EnsureLocalMachine(t, machines)
	plan, err := machines.DeletePlan(context.Background(), machine.LocalID)
	if err != nil || plan.IsBlocked() || len(plan.Removes) == 0 || plan.Removes[0].Kind != deletion.KindAgents {
		t.Fatalf("plan %+v %v", plan, err)
	}
	if err := machines.Remove(context.Background(), machine.LocalID, machine.RemoveOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := machines.Get(context.Background(), machine.LocalID); !errors.Is(err, machine.ErrNotFound) {
		t.Fatalf("after remove: %v", err)
	}
}

// Garder l'historique n'efface rien : le plan qui retient le retrait ne
// retient pas l'archive. La machine, sa sonde et son historique restent, son
// agent est révoqué, elle ne compte plus parmi les machines attendues, et son alerte « perdue » se
// résout puisqu'on n'attend plus d'agent.
func TestRemove_KeepHistory_IsNotHeldBackByItsPlanAndErasesNothing(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	db := storetest.Open(t)
	machines := machine.New(db, machine.NewSessions(), logger)
	alerts := &resolvedAlerter{}
	machines.SetAlerter(alerts)
	storetest.EnsureLocalMachine(t, machines)
	remote := storetest.EnrollMachine(t, machines, remoteID, "vps-paris-1")
	watched, err := probe.New(db, logger).Create(ctx, probe.Definition{Name: "site", Kind: probe.KindHTTP, Target: "https://a.fr/", MachineID: remote.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := machines.Remove(ctx, remote.ID, machine.RemoveOptions{}); !errors.Is(err, machine.ErrHasProbes) {
		t.Fatalf("le retrait entier doit être retenu par la sonde: %v", err)
	}

	totalBefore, _, err := machines.Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := machines.Remove(ctx, remote.ID, machine.RemoveOptions{KeepHistory: true}); err != nil {
		t.Fatal(err)
	}
	if total, _, err := machines.Count(ctx); err != nil || total != totalBefore-1 {
		t.Fatalf("une machine archivée ne compte plus: %d puis %d, %v", totalBefore, total, err)
	}

	archived, err := machines.Get(ctx, remote.ID)
	if err != nil || !archived.Agent.IsRevoked() {
		t.Fatalf("la machine reste, son agent révoqué: %+v %v", archived, err)
	}
	if _, err := db.GetProbe(ctx, watched.ID); err != nil {
		t.Fatalf("la sonde doit rester: %v", err)
	}
	if !reflect.DeepEqual(alerts.back, []string{remote.ID}) || len(alerts.removed) != 0 {
		t.Fatalf("alerte: revenue %v, retirée %v", alerts.back, alerts.removed)
	}
	if err := machines.Remove(ctx, remote.ID, machine.RemoveOptions{KeepHistory: true}); !errors.Is(err, machine.ErrNoAgent) {
		t.Fatalf("archiver une machine archivée: %v", err)
	}
}

type resolvedAlerter struct{ back, removed []string }

func (a *resolvedAlerter) MachineLost(context.Context, machine.Machine) {}
func (a *resolvedAlerter) MachineBack(_ context.Context, machineID string) {
	a.back = append(a.back, machineID)
}
func (a *resolvedAlerter) MachineRemoved(_ context.Context, machineID string) {
	a.removed = append(a.removed, machineID)
}
