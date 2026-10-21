package update_test

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/update"
)

// memoryStore tient lieu de la base : les fiches et les constats qu'on lui
// donne, rien d'autre.
type memoryStore struct {
	containers []container.Container
	checks     []update.Check
}

func (m *memoryStore) ListContainers(_ context.Context, machineID string) ([]container.Container, error) {
	var found []container.Container
	for _, candidate := range m.containers {
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

// recordingRunner tient lieu du flux de l'agent : il garde chaque
// assignation poussée.
type recordingRunner struct{ assignments []update.Assignment }

func (r *recordingRunner) Command(_, name string, payload any) error {
	if assignment, ok := payload.(update.Assignment); ok && name == update.CommandImages {
		r.assignments = append(r.assignments, assignment)
	}
	return nil
}

func fiche(machineID, name, image string, policy container.UpdatePolicy) container.Container {
	return container.Container{
		ID: container.ID(machineID, name), MachineID: machineID, Kind: container.KindContainer, Name: name,
		Image: image, State: container.StateRunning, UpdatePolicy: policy,
	}
}

func newService(store *memoryStore) (*update.Service, *recordingRunner) {
	runner := &recordingRunner{}
	updates := update.New(store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	updates.SetCommander(runner)
	return updates, runner
}

// La politique d'une unité désigne les images que la machine ne doit jamais
// interroger : « ignore » seulement, et une fiche archivée n'en désigne plus.
func TestAssign_ExcludesImagesOfIgnoredUnits(t *testing.T) {
	gone := fiche("m1", "gone", "old:1", container.PolicyIgnore)
	gone.ArchivedAt = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	store := &memoryStore{containers: []container.Container{
		fiche("m1", "ignored", "nginx:1.27", container.PolicyIgnore),
		fiche("m1", "held", "redis:7", container.PolicyHold),
		fiche("m1", "watched", "postgres:16", container.PolicyWatch),
		gone,
		fiche("m2", "elsewhere", "other:1", container.PolicyIgnore),
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
