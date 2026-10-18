package update_test

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/service"
	"github.com/ldesfontaine/opencloud/internal/update"
)

// memoryStore tient lieu de la base : les fiches et les constats qu'on lui
// donne, rien d'autre.
type memoryStore struct {
	services []service.Service
	checks   []update.Check
}

func (m *memoryStore) ListServices(_ context.Context, machineID string) ([]service.Service, error) {
	var found []service.Service
	for _, candidate := range m.services {
		if machineID == "" || candidate.MachineID == machineID {
			found = append(found, candidate)
		}
	}
	return found, nil
}

func (m *memoryStore) ApplyImageChecks(_ context.Context, checks []update.Check) error {
	m.checks = append(m.checks, checks...)
	return nil
}

func (m *memoryStore) ListImageChecks(_ context.Context, machineID string) ([]update.Check, error) {
	var found []update.Check
	for _, check := range m.checks {
		if machineID == "" || check.MachineID == machineID {
			found = append(found, check)
		}
	}
	return found, nil
}

func (m *memoryStore) PurgeImageChecks(context.Context, time.Time) error { return nil }

type recordingRunner struct{ assignments []update.Assignment }

func (r *recordingRunner) Assign(assignment update.Assignment) {
	r.assignments = append(r.assignments, assignment)
}

func fiche(machineID, name, image string, policy service.UpdatePolicy) service.Service {
	return service.Service{
		ID: service.ID(machineID, name), MachineID: machineID, Kind: service.KindContainer, Name: name,
		Image: image, State: service.StateRunning, UpdatePolicy: policy,
	}
}

func newService(store *memoryStore) (*update.Service, *recordingRunner) {
	runner := &recordingRunner{}
	updates := update.New(store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	updates.SetLocalRunner("m1", runner)
	return updates, runner
}

// La politique d'une unité désigne les images que la machine ne doit jamais
// interroger : « ignore » seulement, et une fiche archivée n'en désigne plus.
func TestAssign_ExcludesImagesOfIgnoredUnits(t *testing.T) {
	gone := fiche("m1", "gone", "old:1", service.PolicyIgnore)
	gone.ArchivedAt = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	store := &memoryStore{services: []service.Service{
		fiche("m1", "ignored", "nginx:1.27", service.PolicyIgnore),
		fiche("m1", "held", "redis:7", service.PolicyHold),
		fiche("m1", "watched", "postgres:16", service.PolicyWatch),
		gone,
		fiche("m2", "elsewhere", "other:1", service.PolicyIgnore),
	}}
	updates, runner := newService(store)

	updates.Assign(context.Background(), "m1")

	if len(runner.assignments) != 1 {
		t.Fatalf("assignations = %+v", runner.assignments)
	}
	if got := runner.assignments[0]; !slices.Equal(got.Excluded, []string{"nginx:1.27"}) || got.Now {
		t.Fatalf("assignation = %+v", got)
	}
}

// Épinglée (hold), une mise à jour est vérifiée mais ni montrée ni comptée ;
// ignorée, elle n'est pas comptée non plus ; seule « watch » compte.
func TestCount_SkipsHeldAndIgnored(t *testing.T) {
	store := &memoryStore{
		services: []service.Service{
			fiche("m1", "watched", "nginx:1.27", service.PolicyWatch),
			fiche("m1", "held", "redis:7", service.PolicyHold),
			fiche("m1", "ignored", "postgres:16", service.PolicyIgnore),
			fiche("m1", "uptodate", "caddy:2", service.PolicyWatch),
		},
		checks: []update.Check{
			{MachineID: "m1", Image: "nginx:1.27", Kind: update.KindMinor, NewerTag: "1.29"},
			{MachineID: "m1", Image: "redis:7", Kind: update.KindMinor, NewerTag: "7.4"},
			{MachineID: "m1", Image: "postgres:16", Kind: update.KindPatch, NewerTag: "16.4"},
			{MachineID: "m1", Image: "caddy:2"},
		},
	}
	updates, _ := newService(store)

	count, err := updates.Count(context.Background())

	if err != nil || count != 1 {
		t.Fatalf("Count = %d, %v, want 1", count, err)
	}
}
