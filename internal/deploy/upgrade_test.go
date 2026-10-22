package deploy_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"

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
}

func (w *world) GetDeclaredService(context.Context, string) (directory.Service, error) {
	return w.service, nil
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
	return action.Run{ID: "run1", Kind: order.Kind, ServiceID: order.ServiceID}, nil
}

// upgradeBench : le service est déployé, l'agent a tous ses droits, la machine
// est en ligne, et l'agent propose 16.5 pour la base.
func upgradeBench(t *testing.T, change func(*world, *fakeMachines, *fakeRuns)) (*deploy.Service, *world) {
	t.Helper()
	w := &world{service: nextcloud(), offered: "16.5"}
	machines := &fakeMachines{status: activeMachine("observe,docker.deploy,docker.update")}
	runs := &fakeRuns{folder: succeededRun()}
	if change != nil {
		change(w, machines, runs)
	}
	return deploy.New(w, w, machines, runs, w, slog.New(slog.DiscardHandler)), w
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
			name: "digest pinned", unit: appUnitID, version: "29.0.5", want: deploy.ErrUnitPinnedDigest,
			change: func(w *world, _ *fakeMachines, _ *fakeRuns) { w.offered = "29.0.5" },
		},
		{name: "nothing offered", unit: dbUnitID, version: "16.5", want: deploy.ErrVersionNotOffered, change: func(w *world, _ *fakeMachines, _ *fakeRuns) { w.offered = "" }},
		{name: "another version", unit: dbUnitID, version: "16.6", want: deploy.ErrVersionNotOffered},
		{name: "a space too much", unit: dbUnitID, version: "16.5 ", want: deploy.ErrVersionNotOffered},
		{name: "another case", unit: dbUnitID, version: "V16.5", want: deploy.ErrVersionNotOffered},
		{
			name: "tag the compose refuses", unit: dbUnitID, version: "16.5 --privileged", want: directory.ErrImageInvalid,
			change: func(w *world, _ *fakeMachines, _ *fakeRuns) { w.offered = "16.5 --privileged" },
		},
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
