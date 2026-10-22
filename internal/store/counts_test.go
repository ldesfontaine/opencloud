package store

import (
	"context"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/update"
)

var countsNow = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

// countTestContainer pose une fiche vivante sur la machine openCloud ; le
// test règle ensuite l'état qui l'intéresse.
func countTestContainer(name, image string) container.Container {
	return container.Container{
		ID: container.ID(machine.LocalID, name), MachineID: machine.LocalID, Kind: container.KindContainer, Name: name, ContainerID: name,
		Image: image, State: container.StateRunning, Health: container.HealthHealthy,
		CreatedAt: countsNow, FirstSeenAt: countsNow, LastSeenAt: countsNow,
	}
}

// Un jeu varié : chaque branche de container.NeedsAttention, plus une fiche
// archivée que le compte ignore.
func attentionCases() []container.Container {
	running := countTestContainer("running", "a")
	unhealthy := countTestContainer("unhealthy", "a")
	unhealthy.Health = container.HealthUnhealthy
	starting := countTestContainer("starting", "a")
	starting.Health = container.HealthStarting
	restarting := countTestContainer("restarting", "a")
	restarting.State = container.StateRestarting
	dead := countTestContainer("dead", "a")
	dead.State = container.StateDead
	cleanExit := countTestContainer("clean-exit", "a")
	cleanExit.State, cleanExit.ExitCode = container.StateExited, 0
	stopped := countTestContainer("stopped", "a")
	stopped.State, stopped.ExitCode = container.StateExited, 143
	killed := countTestContainer("killed", "a")
	killed.State, killed.ExitCode = container.StateExited, 137
	crashed := countTestContainer("crashed", "a")
	crashed.State, crashed.ExitCode = container.StateExited, 1
	oom := countTestContainer("oom", "a")
	oom.State, oom.ExitCode, oom.OOMKilled = container.StateExited, 137, true
	manual := countTestContainer("manual-kill", "a")
	manual.State, manual.ExitCode, manual.Killed = container.StateExited, 137, true
	created := countTestContainer("created", "a")
	created.State = container.StateCreated
	paused := countTestContainer("paused", "a")
	paused.State = container.StatePaused
	archived := countTestContainer("archived", "a")
	archived.State, archived.ExitCode, archived.ArchivedAt = container.StateExited, 1, countsNow
	return []container.Container{running, unhealthy, starting, restarting, dead, cleanExit, stopped, killed, crashed, oom, manual, created, paused, archived}
}

// La règle écrite en SQL et celle écrite en Go comptent les mêmes
// conteneurs : si l'une bouge sans l'autre, ce test le dit.
func TestCountContainers_MatchesNeedsAttention(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	declaredOnLocal(t, db)
	seedAgent(t, db, machine.LocalID, countsNow, false)
	cases := attentionCases()
	if err := db.ApplyChanges(ctx, container.Changes{MachineID: machine.LocalID, Now: countsNow, Containers: cases}); err != nil {
		t.Fatal(err)
	}

	total, attention, unknown, err := db.CountContainers(ctx, machine.ObservedSince(countsNow))
	if err != nil || unknown != 0 {
		t.Fatalf("inconnus %d, %v", unknown, err)
	}

	wantTotal, wantAttention := 0, 0
	for _, item := range cases {
		if item.IsArchived() {
			continue
		}
		wantTotal++
		if container.NeedsAttention(item) {
			wantAttention++
		}
	}
	if total != wantTotal || attention != wantAttention {
		t.Fatalf("SQL compte %d/%d, Go compte %d/%d", total, attention, wantTotal, wantAttention)
	}
	if wantAttention != 6 {
		t.Fatalf("le jeu doit couvrir six cas à traiter, pas %d", wantAttention)
	}
}

// seedAgent pose l'agent d'une machine déjà inscrite, vu à l'instant donné
// (zéro : jamais vu), révoqué ou non.
func seedAgent(t *testing.T, db *DB, machineID string, seen time.Time, revoked bool) {
	t.Helper()
	var lastSeen, revokedAt any
	if !seen.IsZero() {
		lastSeen = seen.Unix()
	}
	if revoked {
		revokedAt = countsNow.Unix()
	}
	exec(t, db, `INSERT INTO agents (id, machine_id, public_key, enrolled_at, last_seen_at, revoked_at) VALUES (?, ?, ?, ?, ?, ?)`,
		"agt_"+machineID, machineID, make([]byte, 32), countsNow.Unix(), lastSeen, revokedAt)
}

// Un conteneur en échec qui compte à traiter, sur la machine donnée.
func crashedOn(machineID string) container.Container {
	item := countTestContainer("crashed", "a")
	item.ID, item.MachineID = container.ID(machineID, "crashed"), machineID
	item.State, item.ExitCode = container.StateExited, 1
	return item
}

// Un conteneur dont la machine n'est pas observée (signal trop vieux, agent
// révoqué, pas d'agent) reste compté au total, jamais à traiter : son état
// n'est plus connu, l'alerte « machine perdue » le dit. La borne est incluse,
// comme machine.Observed ; le SQL et la règle en Go comptent pareil.
func TestCountContainers_SkipsMachinesThatAreNotObserved(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	since := machine.ObservedSince(countsNow)
	machines := []struct {
		id      string
		seen    time.Time
		revoked bool
		noAgent bool
	}{
		{id: "fresh", seen: countsNow},
		{id: "edge", seen: since},
		{id: "just-lost", seen: since.Add(-time.Second)},
		{id: "stale", seen: countsNow.Add(-5 * time.Hour)},
		{id: "never-seen"},
		{id: "revoked", seen: countsNow, revoked: true},
		{id: "no-agent", noAgent: true},
	}
	wantAttention := 0
	for _, m := range machines {
		exec(t, db, `INSERT INTO machines (id, name, hostname, created_at) VALUES (?, ?, 'host', ?)`, m.id, m.id, countsNow.Unix())
		if !m.noAgent {
			seedAgent(t, db, m.id, m.seen, m.revoked)
		}
		if err := db.ApplyChanges(ctx, container.Changes{MachineID: m.id, Now: countsNow, Containers: []container.Container{crashedOn(m.id)}}); err != nil {
			t.Fatal(err)
		}
		status := machine.Status{Machine: machine.Machine{LastSeenAt: m.seen}}
		if !m.noAgent {
			status.Agent = machine.Agent{ID: "agt_" + m.id}
			if m.revoked {
				status.Agent.RevokedAt = countsNow
			}
		}
		if machine.Observed(status, countsNow) {
			wantAttention++
		}
	}

	total, attention, unknown, err := db.CountContainers(ctx, since)
	if err != nil {
		t.Fatal(err)
	}
	if total != len(machines) || attention != wantAttention || wantAttention != 2 {
		t.Fatalf("total %d (attendu %d), à traiter %d (attendu %d, soit 2 : fresh et edge)", total, len(machines), attention, wantAttention)
	}
	if unknown != total-wantAttention {
		t.Fatalf("inconnus %d, attendu %d : chaque machine non observée", unknown, total-wantAttention)
	}
}

// Épinglée (hold), une mise à jour est vérifiée mais ni montrée ni comptée ;
// ignorée, elle n'est pas comptée non plus ; seule « watch » compte, et
// une fiche archivée jamais.
func TestCountContainerUpdates_SkipsHeldIgnoredAndArchived(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	declaredOnLocal(t, db)
	stack := directory.Service{
		ID: "svc_stack", MachineID: machine.LocalID, Name: "stack", Nature: directory.NatureApplication,
		Origin: directory.OriginContainer, DeclaredBy: directory.ByOperator, CreatedAt: countsNow, UpdatedAt: countsNow,
	}
	for name, policy := range map[string]container.UpdatePolicy{"held": container.PolicyHold, "ignored": container.PolicyIgnore} {
		stack.Units = append(stack.Units, directory.Unit{
			ID: "unit_" + name, ServiceID: stack.ID, Name: name, Kind: directory.UnitContainer, Image: name + ":1", ContainerName: name, UpdatePolicy: policy,
		})
	}
	if err := db.InsertDeclaredService(ctx, stack); err != nil {
		t.Fatal(err)
	}
	gone := countTestContainer("gone", "gone:1")
	gone.ArchivedAt = countsNow
	containers := []container.Container{
		countTestContainer("watched", "nginx:1.27"), countTestContainer("held", "held:1"), countTestContainer("ignored", "ignored:1"),
		countTestContainer("uptodate", "caddy:2"), gone,
	}
	if err := db.ApplyChanges(ctx, container.Changes{MachineID: machine.LocalID, Now: countsNow, Containers: containers}); err != nil {
		t.Fatal(err)
	}
	err := db.SetContainerUnits(ctx, []container.UnitAssignment{
		{ContainerID: container.ID(machine.LocalID, "held"), UnitID: "unit_held", Level: container.MatchName},
		{ContainerID: container.ID(machine.LocalID, "ignored"), UnitID: "unit_ignored", Level: container.MatchName},
	})
	if err != nil {
		t.Fatal(err)
	}
	checks := []update.Check{
		{MachineID: machine.LocalID, Image: "nginx:1.27", CheckedAt: countsNow, Outcome: update.OutcomeOK, Kind: update.KindMinor, NewerTag: "1.29"},
		{MachineID: machine.LocalID, Image: "held:1", CheckedAt: countsNow, Outcome: update.OutcomeOK, Kind: update.KindMinor, NewerTag: "2"},
		{MachineID: machine.LocalID, Image: "ignored:1", CheckedAt: countsNow, Outcome: update.OutcomeOK, Kind: update.KindPatch, NewerTag: "1.1"},
		{MachineID: machine.LocalID, Image: "caddy:2", CheckedAt: countsNow, Outcome: update.OutcomeOK},
		{MachineID: machine.LocalID, Image: "gone:1", CheckedAt: countsNow, Outcome: update.OutcomeOK, Kind: update.KindMajor, NewerTag: "2"},
	}
	if err := db.ApplyImageChecks(ctx, checks); err != nil {
		t.Fatal(err)
	}

	count, err := db.CountContainerUpdates(ctx)

	if err != nil || count != 1 {
		t.Fatalf("CountContainerUpdates = %d, %v, want 1", count, err)
	}
}
