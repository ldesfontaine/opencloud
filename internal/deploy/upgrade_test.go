package deploy_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/deploy"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

const binaryUnitID = "unt_00000000000000a3"

// world tient l'annuaire et le journal des appels : l'ordre des écritures et
// de l'envoi se lit dans calls.
type world struct {
	service  directory.Service
	offered  string
	calls    []string
	setErr   error
	sendErr  error
	sentJSON []json.RawMessage
	// Les montées de version gardées, par run.
	updates map[string]deploy.ServiceUpdate
}

func (w *world) InsertServiceUpdate(_ context.Context, update deploy.ServiceUpdate) error {
	if w.updates == nil {
		w.updates = map[string]deploy.ServiceUpdate{}
	}
	w.updates[update.RunID] = update
	return nil
}

func (w *world) SettleServiceUpdate(_ context.Context, runID string, state deploy.UpdateState, reason string, _ time.Time) (deploy.ServiceUpdate, bool, error) {
	update, found := w.updates[runID]
	if !found || update.State != deploy.UpdatePending {
		return deploy.ServiceUpdate{}, false, nil
	}
	update.State, update.Reason = state, reason
	w.updates[runID] = update
	return update, true, nil
}

func (w *world) FindPendingServiceUpdate(_ context.Context, machineID string, _ time.Time) (deploy.ServiceUpdate, bool, error) {
	for _, update := range w.updates {
		if update.MachineID == machineID && update.State == deploy.UpdatePending {
			return update, true, nil
		}
	}
	return deploy.ServiceUpdate{}, false, nil
}

func (w *world) LatestServiceUpdate(_ context.Context, serviceID string) (deploy.ServiceUpdate, bool, error) {
	for _, update := range w.updates {
		if update.ServiceID == serviceID {
			return update, true, nil
		}
	}
	return deploy.ServiceUpdate{}, false, nil
}

func (w *world) GetDeclaredService(context.Context, string) (directory.Service, error) {
	return w.service, nil
}

func (w *world) ListDeclaredServices(context.Context, directory.Filter) ([]directory.Service, error) {
	return []directory.Service{w.service}, nil
}

func (w *world) GetUnit(_ context.Context, id string) (directory.Unit, error) {
	return fakeServices{service: w.service}.GetUnit(context.Background(), id)
}

func (w *world) OfferedVersion(_ context.Context, unitID string) (string, bool, error) {
	return w.offered, w.offered != "", nil
}

func (w *world) SetUnitVersion(_ context.Context, unitID, version string) error {
	if w.setErr != nil {
		return w.setErr
	}
	w.calls = append(w.calls, "set "+unitID+" "+version)
	for index := range w.service.Units {
		if w.service.Units[index].ID == unitID {
			w.service.Units[index].Version = version
		}
	}
	return nil
}

func (w *world) SendForService(_ context.Context, order action.ServiceOrder) (action.Run, error) {
	if w.sendErr != nil {
		return action.Run{}, w.sendErr
	}
	w.calls = append(w.calls, "send "+string(order.Kind))
	w.sentJSON = append(w.sentJSON, order.Params)
	if order.Secret != nil {
		w.calls = append(w.calls, "secret")
	}
	return action.Run{ID: "run1", Kind: order.Kind, ServiceID: order.ServiceID, MachineID: order.MachineID}, nil
}

// upgradeBench : le service est déployé, l'agent a tous ses droits, la machine
// est en ligne, et l'agent propose 16.5 pour la base.
func upgradeBench(t *testing.T, change func(*world, *fakeMachines, *fakeRuns)) (*deploy.Service, *world) {
	t.Helper()
	w := &world{service: nextcloud(), offered: "16.5"}
	machines := &fakeMachines{status: activeMachine("observe,docker.deploy,docker.update")}
	machines.status.Agent.ServiceRollbackVersion = 1
	runs := &fakeRuns{folder: succeededRun()}
	if change != nil {
		change(w, machines, runs)
	}
	return deploy.New(w, w, machines, runs, w, w, slog.New(slog.DiscardHandler)), w
}

// Le geste écrit la version, puis envoie le redéploiement dont le compose
// porte le nouveau tag, sans .env.
func TestUpgrade_WritesTheVersionThenRedeploys(t *testing.T) {
	service, w := upgradeBench(t, nil)

	run, err := service.Upgrade(context.Background(), dbUnitID, "16.5")

	if err != nil || run.Kind != agentwire.KindDockerUpdate {
		t.Fatalf("run %+v, err %v", run, err)
	}
	want := []string{"set " + dbUnitID + " 16.5", "send docker.update"}
	if !slices.Equal(w.calls, want) {
		t.Fatalf("calls %v, want %v: the version is written before the order leaves, and no .env goes", w.calls, want)
	}
	var params agentwire.DockerParams
	if err := json.Unmarshal(w.sentJSON[0], &params); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(params.Compose, "image: postgres:16.5\n") || strings.Contains(params.Compose, "postgres:16.4") || params.EnvSent {
		t.Fatalf("compose of the order:\n%s", params.Compose)
	}
}

// La montée part avec son filet et reste gardée : de quelle version à quelle
// version, en attente.
func TestUpgrade_SendsTheSafetyNetAndKeepsTheChange(t *testing.T) {
	service, w := upgradeBench(t, nil)
	if _, err := service.Upgrade(context.Background(), dbUnitID, "16.5"); err != nil {
		t.Fatal(err)
	}
	var params agentwire.DockerParams
	if err := json.Unmarshal(w.sentJSON[0], &params); err != nil {
		t.Fatal(err)
	}
	if params.Rollback == nil || *params.Rollback != agentwire.DefaultServiceRollback() || !params.Rollback.IsValid() {
		t.Fatalf("safety net %+v", params.Rollback)
	}
	update := w.updates["run1"]
	if update.State != deploy.UpdatePending || update.FromVersion != "16.4" || update.ToVersion != "16.5" || update.UnitID != dbUnitID || update.ServiceID != serviceID {
		t.Fatalf("update %+v", update)
	}
	// Un redéploiement simple n'a pas de filet.
	if _, err := service.Redeploy(context.Background(), serviceID, nil); err != nil {
		t.Fatal(err)
	}
	var plain agentwire.DockerParams
	if err := json.Unmarshal(w.sentJSON[1], &plain); err != nil || plain.Rollback != nil {
		t.Fatalf("a redeployment carries a safety net: %+v %v", plain.Rollback, err)
	}
}

// Ce que l'agent rapporte d'une montée remet, ou non, la version d'avant dans
// l'annuaire.
func TestSettleUpdate_PutsThePreviousVersionBackOnlyWhenTheMachineWentBack(t *testing.T) {
	cases := []struct {
		name     string
		run      action.Run
		state    deploy.UpdateState
		restores bool
		failed   bool
	}{
		{"rolled back", action.Run{State: action.StateFailed, Reason: agentwire.ReasonRolledBackVersion}, deploy.UpdateRolledBack, true, false},
		{"return failed too", action.Run{State: action.StateFailed, Reason: agentwire.ReasonRollbackFailed}, deploy.UpdateFailed, true, true},
		{"refused before writing", action.Run{State: action.StateRefused, Reason: agentwire.ReasonDiskFull}, deploy.UpdateRefused, true, false},
		{"confirmed", action.Run{State: action.StateSucceeded}, deploy.UpdateConfirmed, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			service, w := upgradeBench(t, nil)
			if _, err := service.Upgrade(context.Background(), dbUnitID, "16.5"); err != nil {
				t.Fatal(err)
			}
			w.calls = nil
			c.run.ID, c.run.Kind = "run1", agentwire.KindDockerUpdate

			end, found, err := service.SettleUpdate(context.Background(), c.run)

			if err != nil || !found {
				t.Fatalf("found %v, err %v", found, err)
			}
			if w.updates["run1"].State != c.state {
				t.Fatalf("state %q, want %q", w.updates["run1"].State, c.state)
			}
			restored := slices.Equal(w.calls, []string{"set " + dbUnitID + " 16.4"})
			if restored != c.restores || (!c.restores && len(w.calls) != 0) {
				t.Fatalf("calls %v, restores want %v", w.calls, c.restores)
			}
			if end.Confirmed != (c.state == deploy.UpdateConfirmed) || end.Failed != c.failed || end.FromVersion != "16.4" || end.ToVersion != "16.5" {
				t.Fatalf("end %+v", end)
			}
			// Un rapport rejoué ne ferme rien deux fois.
			w.calls = nil
			if _, again, _ := service.SettleUpdate(context.Background(), c.run); again || len(w.calls) != 0 {
				t.Fatalf("a replayed report settled twice: %v", w.calls)
			}
		})
	}
}

func TestSettleUpdate_IgnoresWhatIsNotAnUpdateInTheMaking(t *testing.T) {
	service, w := upgradeBench(t, nil)
	if _, err := service.Upgrade(context.Background(), dbUnitID, "16.5"); err != nil {
		t.Fatal(err)
	}
	w.calls = nil
	for name, run := range map[string]action.Run{
		"still accepted": {ID: "run1", Kind: agentwire.KindDockerUpdate, State: action.StateAccepted},
		"timed out":      {ID: "run1", Kind: agentwire.KindDockerUpdate, State: action.StateFailed, Reason: "timeout"},
		"another kind":   {ID: "run1", Kind: agentwire.KindDockerDeploy, State: action.StateFailed, Reason: agentwire.ReasonRolledBackVersion},
		"a redeployment": {ID: "run9", Kind: agentwire.KindDockerUpdate, State: action.StateFailed, Reason: agentwire.ReasonRolledBackVersion},
	} {
		if _, found, err := service.SettleUpdate(context.Background(), run); found || err != nil {
			t.Errorf("%s: settled (%v, %v)", name, found, err)
		}
	}
	if len(w.calls) != 0 || w.updates["run1"].State != deploy.UpdatePending {
		t.Fatalf("calls %v, state %q", w.calls, w.updates["run1"].State)
	}
}

func TestRollBack_AsksTheAgentForTheProjectOnly(t *testing.T) {
	service, w := upgradeBench(t, nil)
	if _, err := service.RollBack(context.Background(), serviceID); err != nil {
		t.Fatal(err)
	}
	var params map[string]any
	if err := json.Unmarshal(w.sentJSON[0], &params); err != nil || len(params) != 1 || params["project"] != "nextcloud" {
		t.Fatalf("params %v %v", params, err)
	}
	if !slices.Equal(w.calls, []string{"send docker.rollback"}) {
		t.Fatalf("calls %v", w.calls)
	}
}

// Chaque refus se juge avant l'écriture : rien n'est écrit, rien ne part.
func TestUpgrade_EachRefusalWritesNothing(t *testing.T) {
	cases := []struct {
		name    string
		unit    string
		version string
		change  func(*world, *fakeMachines, *fakeRuns)
		want    error
	}{
		{name: "unknown unit", unit: "unt_inconnue", version: "16.5", want: directory.ErrUnitNotFound},
		{
			name: "binary unit", unit: binaryUnitID, version: "16.5", want: directory.ErrUnitNotContainer,
			change: func(w *world, _ *fakeMachines, _ *fakeRuns) {
				w.service.Units = append(w.service.Units, directory.Unit{ID: binaryUnitID, ServiceID: serviceID, Name: "agent", Kind: directory.UnitBinary, SystemdUnit: "agent.service"})
			},
		},
		{
			name: "never deployed", unit: dbUnitID, version: "16.5", want: deploy.ErrNotDeployed,
			change: func(_ *world, _ *fakeMachines, runs *fakeRuns) { runs.folder = nil },
		},
		{
			name: "right missing", unit: dbUnitID, version: "16.5", want: deploy.ErrRightMissing,
			change: func(_ *world, machines *fakeMachines, _ *fakeRuns) {
				machines.status = activeMachine("observe,docker.deploy")
			},
		},
		{
			name: "machine offline", unit: dbUnitID, version: "16.5", want: action.ErrMachineOffline,
			change: func(_ *world, machines *fakeMachines, _ *fakeRuns) { machines.status.Online = false },
		},
		{
			name: "no agent", unit: dbUnitID, version: "16.5", want: deploy.ErrNoAgent,
			change: func(_ *world, machines *fakeMachines, _ *fakeRuns) {
				machines.status.Agent.RevokedAt = machines.status.Agent.EnrolledAt
			},
		},
		{
			name: "agent cannot go back by itself", unit: dbUnitID, version: "16.5", want: deploy.ErrRollbackUnavailable,
			change: func(_ *world, machines *fakeMachines, _ *fakeRuns) { machines.status.Agent.ServiceRollbackVersion = 0 },
		},
		{
			name: "another change is being judged on the machine", unit: dbUnitID, version: "16.5", want: deploy.ErrUpdateInProgress,
			change: func(w *world, _ *fakeMachines, _ *fakeRuns) {
				w.updates = map[string]deploy.ServiceUpdate{"run0": {RunID: "run0", MachineID: "m1", State: deploy.UpdatePending}}
			},
		},
		{
			name: "digest pinned", unit: appUnitID, version: "29.0.5", want: deploy.ErrUnitPinnedDigest,
			change: func(w *world, _ *fakeMachines, _ *fakeRuns) { w.offered = "29.0.5" },
		},
		{name: "nothing offered", unit: dbUnitID, version: "16.5", want: deploy.ErrVersionNotOffered, change: func(w *world, _ *fakeMachines, _ *fakeRuns) { w.offered = "" }},
		{name: "another version", unit: dbUnitID, version: "16.6", want: deploy.ErrVersionNotOffered},
		{name: "a space too much", unit: dbUnitID, version: "16.5 ", want: deploy.ErrVersionNotOffered},
		{name: "another case", unit: dbUnitID, version: "V16.5", want: deploy.ErrVersionNotOffered},
		{
			name: "tag the compose refuses", unit: dbUnitID, version: "16.5 --privileged", want: directory.ErrVersionInvalid,
			change: func(w *world, _ *fakeMachines, _ *fakeRuns) { w.offered = "16.5 --privileged" },
		},
	}
	// Un tag hostile que l'annuaire aurait offert (registre piégé, ligne
	// ancienne) : refusé avant le rendu, même égal à l'offre. « : » et « / »
	// passeraient le motif du compose, qui les lit comme le nom de l'image.
	for _, hostile := range []string{
		"16.5$x", "16.5${POSTGRES_PASSWORD}", "16.5#x", "16.5\nprivileged: true", "16.5:x", "evil.example/x:1",
		"16.5@sha256:" + strings.Repeat("0", 64), "latest", "LATEST", ".16", "-16", strings.Repeat("1", 129),
	} {
		want := directory.ErrVersionInvalid
		if strings.EqualFold(hostile, "latest") {
			want = directory.ErrVersionLatest
		}
		cases = append(cases, struct {
			name    string
			unit    string
			version string
			change  func(*world, *fakeMachines, *fakeRuns)
			want    error
		}{
			name: "hostile " + hostile, unit: dbUnitID, version: hostile, want: want,
			change: func(w *world, _ *fakeMachines, _ *fakeRuns) { w.offered = hostile },
		})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			service, w := upgradeBench(t, c.change)
			before := nextcloud().Units[1].Version

			run, err := service.Upgrade(context.Background(), c.unit, c.version)

			if !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
			if len(w.calls) != 0 || run.ID != "" || w.service.Units[1].Version != before {
				t.Fatalf("a refusal wrote or sent: %v %+v %q", w.calls, run, w.service.Units[1].Version)
			}
		})
	}
}

// Les refus qui viennent de l'état portent une clé que l'interface traduit.
func TestUpgrade_RefusalsCarryTheirKey(t *testing.T) {
	for want, err := range map[string]error{
		"deploy.version_not_offered":   deploy.ErrVersionNotOffered,
		"deploy.unit_pinned_digest":    deploy.ErrUnitPinnedDigest,
		"directory.unit_not_container": directory.ErrUnitNotContainer,
	} {
		if key, ok := refusal.KeyOf(err); !ok || key != want {
			t.Errorf("key %q, want %q", key, want)
		}
	}
	if !refusal.IsConflict(deploy.ErrVersionNotOffered) || !refusal.IsConflict(deploy.ErrUnitPinnedDigest) {
		t.Error("a state refusal is a conflict (409)")
	}
}

// L'envoi échoue après l'écriture : la version reste, l'erreur revient à
// l'opérateur, qui redéploie.
func TestUpgrade_AFailedSendKeepsTheVersionAndSaysSo(t *testing.T) {
	service, w := upgradeBench(t, func(w *world, _ *fakeMachines, _ *fakeRuns) { w.sendErr = action.ErrMachineOffline })

	_, err := service.Upgrade(context.Background(), dbUnitID, "16.5")

	if !errors.Is(err, action.ErrMachineOffline) {
		t.Fatalf("got %v", err)
	}
	if w.service.Units[1].Version != "16.5" || !slices.Equal(w.calls, []string{"set " + dbUnitID + " 16.5"}) {
		t.Fatalf("version %q, calls %v", w.service.Units[1].Version, w.calls)
	}
}

// Si l'annuaire refuse l'écriture, rien ne part.
func TestUpgrade_ARefusedWriteSendsNothing(t *testing.T) {
	service, w := upgradeBench(t, func(w *world, _ *fakeMachines, _ *fakeRuns) { w.setErr = directory.ErrUnitNotFound })

	if _, err := service.Upgrade(context.Background(), dbUnitID, "16.5"); !errors.Is(err, directory.ErrUnitNotFound) {
		t.Fatalf("got %v", err)
	}
	if len(w.calls) != 0 {
		t.Fatalf("calls %v", w.calls)
	}
}

// Une montée en attente depuis plus d'une heure (le run a expiré, l'agent n'a
// rien rapporté) ne reste pas « en cours » à l'écran, et ne bloque plus la
// machine.
func TestPlan_APendingUpdateOlderThanTheRunWindowIsNotInProgress(t *testing.T) {
	service, w := upgradeBench(t, nil)
	w.updates = map[string]deploy.ServiceUpdate{"old": {
		RunID: "old", ServiceID: serviceID, MachineID: "m1", State: deploy.UpdatePending, RequestedAt: time.Now().Add(-2 * time.Hour),
	}}
	plan, err := service.Plan(context.Background(), serviceID)
	if err != nil || plan.LastUpdate == nil || plan.LastUpdate.State != deploy.UpdateFailed {
		t.Fatalf("plan %+v %v", plan.LastUpdate, err)
	}
	w.updates["old"] = deploy.ServiceUpdate{RunID: "old", ServiceID: serviceID, MachineID: "m1", State: deploy.UpdatePending, RequestedAt: time.Now().Add(-time.Minute)}
	if plan, err = service.Plan(context.Background(), serviceID); err != nil || plan.LastUpdate.State != deploy.UpdatePending {
		t.Fatalf("a recent one: %+v %v", plan.LastUpdate, err)
	}
}

// Un rapport tardif ferme la montée sans toucher à la version déclarée.
func TestSettleLateUpdate_ClosesTheChangeWithoutPuttingTheVersionBack(t *testing.T) {
	service, w := upgradeBench(t, nil)
	if _, err := service.Upgrade(context.Background(), dbUnitID, "16.5"); err != nil {
		t.Fatal(err)
	}
	w.calls = nil
	end, found, err := service.SettleLateUpdate(context.Background(), "run1", agentwire.ReasonRolledBackVersion)
	if err != nil || !found || !end.Late || !end.Reverted || end.Failed {
		t.Fatalf("end %+v %v %v", end, found, err)
	}
	if len(w.calls) != 0 || w.updates["run1"].State != deploy.UpdateRolledBack {
		t.Fatalf("calls %v, state %q: the directory must not be touched", w.calls, w.updates["run1"].State)
	}
	if _, again, _ := service.SettleLateUpdate(context.Background(), "run1", agentwire.ReasonRolledBackVersion); again {
		t.Fatal("a late report settled twice")
	}
}
