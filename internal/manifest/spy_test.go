package manifest_test

import (
	"context"
	"sync"

	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/manifest"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// errInjected est un refus : l'échec au milieu se journalise sous sa clé,
// comme celui d'un vrai composant.
var errInjected = refusal.Invalid("test.injected", "injected failure")

// spy compte les gestes d'écriture du fichier, et en fait échouer un au rang
// demandé (failAt, à partir de 1) : le faux composant du test d'échec au
// milieu.
type spy struct {
	mu     sync.Mutex
	calls  []string
	failAt int
	// before s'exécute avant l'écriture numéro n, sans verrou : un test y fait
	// ce qu'un autre opérateur ferait au même moment.
	hook func(call int)
}

func (s *spy) record(call string) error {
	s.mu.Lock()
	s.calls = append(s.calls, call)
	count, failAt, hook := len(s.calls), s.failAt, s.hook
	s.mu.Unlock()
	if hook != nil {
		hook(count)
	}
	if failAt > 0 && count == failAt {
		return errInjected
	}
	return nil
}

func (s *spy) written() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

func (s *spy) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls, s.failAt, s.hook = nil, 0, nil
}

type spyDirectory struct {
	manifest.Directory
	spy *spy
}

func (d spyDirectory) CreateProject(ctx context.Context, definition directory.ProjectDefinition) (directory.Project, error) {
	if err := d.spy.record("CreateProject " + definition.Name); err != nil {
		return directory.Project{}, err
	}
	return d.Directory.CreateProject(ctx, definition)
}

func (d spyDirectory) UpdateProject(ctx context.Context, id string, definition directory.ProjectDefinition) (directory.Project, error) {
	if err := d.spy.record("UpdateProject " + definition.Name); err != nil {
		return directory.Project{}, err
	}
	return d.Directory.UpdateProject(ctx, id, definition)
}

func (d spyDirectory) DeleteProject(ctx context.Context, id string) error {
	if err := d.spy.record("DeleteProject " + id); err != nil {
		return err
	}
	return d.Directory.DeleteProject(ctx, id)
}

func (d spyDirectory) CreateService(ctx context.Context, definition directory.ServiceDefinition) (directory.Service, error) {
	if err := d.spy.record("CreateService " + definition.Name); err != nil {
		return directory.Service{}, err
	}
	return d.Directory.CreateService(ctx, definition)
}

func (d spyDirectory) UpdateService(ctx context.Context, id string, definition directory.ServiceDefinition) (directory.Service, error) {
	if err := d.spy.record("UpdateService " + definition.Name); err != nil {
		return directory.Service{}, err
	}
	return d.Directory.UpdateService(ctx, id, definition)
}

func (d spyDirectory) DeleteService(ctx context.Context, id string) error {
	if err := d.spy.record("DeleteService " + id); err != nil {
		return err
	}
	return d.Directory.DeleteService(ctx, id)
}

func (d spyDirectory) Ignore(ctx context.Context, key directory.IgnoredKey) error {
	if err := d.spy.record("Ignore " + key.Value); err != nil {
		return err
	}
	return d.Directory.Ignore(ctx, key)
}

func (d spyDirectory) Unignore(ctx context.Context, key directory.IgnoredKey) error {
	if err := d.spy.record("Unignore " + key.Value); err != nil {
		return err
	}
	return d.Directory.Unignore(ctx, key)
}

type spyProbes struct {
	manifest.Probes
	spy *spy
}

func (p spyProbes) Create(ctx context.Context, definition probe.Definition) (probe.Probe, error) {
	if err := p.spy.record("CreateProbe " + definition.Name); err != nil {
		return probe.Probe{}, err
	}
	return p.Probes.Create(ctx, definition)
}

func (p spyProbes) Update(ctx context.Context, id string, definition probe.Definition) (probe.Probe, error) {
	if err := p.spy.record("UpdateProbe " + definition.Name); err != nil {
		return probe.Probe{}, err
	}
	return p.Probes.Update(ctx, id, definition)
}

func (p spyProbes) Delete(ctx context.Context, id string) error {
	if err := p.spy.record("DeleteProbe " + id); err != nil {
		return err
	}
	return p.Probes.Delete(ctx, id)
}

type spyHeartbeats struct {
	manifest.Heartbeats
	spy *spy
}

func (h spyHeartbeats) Create(ctx context.Context, definition heartbeat.Definition) (heartbeat.Heartbeat, error) {
	if err := h.spy.record("CreateHeartbeat " + definition.Name); err != nil {
		return heartbeat.Heartbeat{}, err
	}
	return h.Heartbeats.Create(ctx, definition)
}

func (h spyHeartbeats) Update(ctx context.Context, id string, definition heartbeat.Definition) (heartbeat.Heartbeat, error) {
	if err := h.spy.record("UpdateHeartbeat " + definition.Name); err != nil {
		return heartbeat.Heartbeat{}, err
	}
	return h.Heartbeats.Update(ctx, id, definition)
}

func (h spyHeartbeats) Delete(ctx context.Context, id string) error {
	if err := h.spy.record("DeleteHeartbeat " + id); err != nil {
		return err
	}
	return h.Heartbeats.Delete(ctx, id)
}
