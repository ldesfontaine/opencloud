package store

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/sampler"
	"github.com/ldesfontaine/opencloud/internal/seal"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	db, err := Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	db.SetSealer(seal.Generate())
	t.Cleanup(func() { db.Close() })
	return db
}

func newKey(t *testing.T) []byte {
	t.Helper()
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return public
}

func issueToken(t *testing.T, db *DB, name, machineID string, now time.Time) string {
	t.Helper()
	cleartext, token, err := machine.NewToken(name, machineID)
	if err != nil {
		t.Fatal(err)
	}
	token.CreatedAt = now
	token.ExpiresAt = now.Add(time.Hour)
	if err := db.InsertToken(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	return cleartext
}

func candidate(t *testing.T) machine.Machine {
	t.Helper()
	id, err := machine.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return machine.Machine{ID: id, Hostname: "vps", OS: "Debian 12", Arch: "amd64"}
}

// newAgent tire l'agent qu'un enrôlement inscrit : un identifiant, une clé.
func newAgent(t *testing.T, at time.Time) machine.Agent {
	t.Helper()
	id, err := machine.NewAgentID()
	if err != nil {
		t.Fatal(err)
	}
	return machine.Agent{ID: id, PublicKey: newKey(t), Version: "v0.0.1", EnrolledAt: at}
}

func TestOpen_AppliesMigrationsOnceAndSurvivesReopen(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	ctx := context.Background()
	db, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	versions, err := db.AppliedVersions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) == 0 || versions[0] != 1 {
		t.Fatalf("applied %v", versions)
	}
	db.Close()

	again, err := Open(ctx, root)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer again.Close()
	if _, err := again.ListMachines(ctx); err != nil {
		t.Fatal(err)
	}
}

// Une base d'avant les agents garde la ligne de la machine openCloud, sans
// agent : l'agent que le service reconnaît comme celui du serveur arrive
// sous LocalID et la reprend, nom, date et historique compris. Tant qu'il
// est actif, un second est refusé et le jeton reste valable.
func TestEnroll_UnderLocalID_AdoptsTheRowLeftWithoutAgent(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	exec(t, db, `INSERT INTO machines (id, name, hostname, created_at) VALUES (?, 'opencloud', 'old-host', 1)`, machine.LocalID)
	local := candidate(t)
	local.ID = machine.LocalID
	token := issueToken(t, db, "oc-host", "", now)
	adopted, err := db.Enroll(ctx, machine.HashToken(token), local, newAgent(t, now), now)
	if err != nil {
		t.Fatal(err)
	}
	if adopted.ID != machine.LocalID || adopted.Name != "opencloud" || adopted.Hostname != "vps" || !adopted.CreatedAt.Equal(time.Unix(1, 0)) || !adopted.Agent.IsActive() {
		t.Fatalf("adopted %+v", adopted)
	}
	twin := issueToken(t, db, "twin", "", now)
	if _, err := db.Enroll(ctx, machine.HashToken(twin), local, newAgent(t, now), now); !errors.Is(err, machine.ErrLocalAgentActive) {
		t.Fatalf("a second agent under LocalID: %v", err)
	}
	if pending, _ := db.ListPendingTokens(ctx, now); len(pending) != 1 || pending[0].Name != "twin" {
		t.Fatalf("the refused token must stay pending: %+v", pending)
	}
	// Sans ligne, l'agent du serveur la crée, au nom du jeton.
	fresh := openTestDB(t)
	created, err := fresh.Enroll(ctx, machine.HashToken(issueToken(t, fresh, "oc-host", "", now)), local, newAgent(t, now), now)
	if err != nil || created.ID != machine.LocalID || created.Name != "oc-host" {
		t.Fatalf("created %+v %v", created, err)
	}
}

// Sur une base d'avant les agents, la ligne offerte pour la machine
// openCloud est un ré-enrôlement de sa ligne : elle garde son nom.
func TestOfferLocalEnrollment_OnAnUpgradedBase_ReenrollsTheExistingRow(t *testing.T) {
	db := openTestDB(t)
	exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES (?, 'opencloud', 1)`, machine.LocalID)
	machines := machine.New(db, machine.NewSessions(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	machines.SetServerHost("sha256:server", "oc-host")
	_, token, err := machines.OfferLocalEnrollment(context.Background())
	if err != nil || token.MachineID != machine.LocalID || token.Name != "opencloud" {
		t.Fatalf("offer %+v %v", token, err)
	}
}

func TestEnroll_ConsumesTheTokenAndCreatesTheMachine(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	token := issueToken(t, db, "vps-paris-1", "", now)
	wanted := candidate(t)

	agent := newAgent(t, now)
	enrolled, err := db.Enroll(ctx, machine.HashToken(token), wanted, agent, now)
	if err != nil {
		t.Fatal(err)
	}
	if enrolled.ID != wanted.ID || enrolled.Name != "vps-paris-1" || enrolled.Agent.ID != agent.ID || !enrolled.Agent.IsActive() {
		t.Fatalf("got %+v", enrolled)
	}
	if string(enrolled.Agent.PublicKey) != string(agent.PublicKey) || enrolled.Agent.Version != "v0.0.1" || enrolled.Agent.RootDir != "/srv/workspace" {
		t.Fatalf("agent %+v", enrolled.Agent)
	}
	pending, err := db.ListPendingTokens(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("token still pending: %+v", pending)
	}
	if _, err := db.Enroll(ctx, machine.HashToken(token), candidate(t), newAgent(t, now), now); !errors.Is(err, machine.ErrTokenConsumed) {
		t.Fatalf("second use: %v", err)
	}
}

func TestEnroll_ClassifiesEveryFailure(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)

	if _, err := db.Enroll(ctx, machine.HashToken("oc_nope"), candidate(t), newAgent(t, now), now); !errors.Is(err, machine.ErrTokenNotFound) {
		t.Errorf("unknown: %v", err)
	}
	expired := issueToken(t, db, "old", "", now.Add(-2*time.Hour))
	if _, err := db.Enroll(ctx, machine.HashToken(expired), candidate(t), newAgent(t, now), now); !errors.Is(err, machine.ErrTokenExpired) {
		t.Errorf("expired: %v", err)
	}
	first := issueToken(t, db, "same-name", "", now)
	if _, err := db.Enroll(ctx, machine.HashToken(first), candidate(t), newAgent(t, now), now); err != nil {
		t.Fatal(err)
	}
	second := issueToken(t, db, "same-name", "", now)
	if _, err := db.Enroll(ctx, machine.HashToken(second), candidate(t), newAgent(t, now), now); !errors.Is(err, machine.ErrNameTaken) {
		t.Errorf("name taken: %v", err)
	}
	// Le jeton refusé pour un nom pris n'est pas consommé : l'opérateur peut
	// retirer l'ancienne machine et réessayer.
	pending, err := db.ListPendingTokens(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending %+v", pending)
	}
}

func TestEnroll_SameTokenConcurrently_OnlyOneWins(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	token := issueToken(t, db, "raced", "", now)

	const racers = 8
	var wins int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := db.Enroll(ctx, machine.HashToken(token), candidate(t), newAgent(t, now), now); err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d enrollments succeeded, want 1", wins)
	}
}

func TestEnroll_WithMachineID_ReplacesTheKeyAndKeepsTheID(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	first := issueToken(t, db, "vps-lyon-2", "", now)
	original, err := db.Enroll(ctx, machine.HashToken(first), candidate(t), newAgent(t, now), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.TouchAgent(ctx, original.Agent.ID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	again := issueToken(t, db, "vps-lyon-2", original.ID, now)
	replacement := candidate(t)
	later := now.Add(30 * time.Minute)
	agent := newAgent(t, later)
	reenrolled, err := db.Enroll(ctx, machine.HashToken(again), replacement, agent, later)
	if err != nil {
		t.Fatal(err)
	}
	if reenrolled.ID != original.ID || reenrolled.Agent.ID != agent.ID || string(reenrolled.Agent.PublicKey) != string(agent.PublicKey) {
		t.Fatalf("got %+v", reenrolled)
	}
	if !reenrolled.LastSeenAt.IsZero() || !reenrolled.CreatedAt.Equal(original.CreatedAt) {
		t.Fatalf("history not reset as expected: %+v", reenrolled)
	}
	if _, err := db.GetMachine(ctx, replacement.ID); !errors.Is(err, machine.ErrNotFound) {
		t.Fatalf("the agent's new id must not become a machine: %v", err)
	}
	// L'agent d'avant reste, révoqué : un seul actif par machine.
	if active := count(t, db, `SELECT COUNT(*) FROM agents WHERE machine_id = ? AND revoked_at IS NULL`, original.ID); active != 1 {
		t.Fatalf("%d active agents", active)
	}
	if revoked := count(t, db, `SELECT COUNT(*) FROM agents WHERE id = ? AND revoked_at = ?`, original.Agent.ID, later.Unix()); revoked != 1 {
		t.Fatal("the previous agent must be revoked at the re-enrollment")
	}
}

func TestDeleteMachine_CascadesItsAgentsAndTokens(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	token := issueToken(t, db, "gone", "", now)
	enrolled, err := db.Enroll(ctx, machine.HashToken(token), candidate(t), newAgent(t, now), now)
	if err != nil {
		t.Fatal(err)
	}
	issueToken(t, db, "gone", enrolled.ID, now)
	if err := db.DeleteMachine(ctx, enrolled.ID); err != nil {
		t.Fatal(err)
	}
	pending, err := db.ListPendingTokens(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("re-enroll token survived its machine: %+v", pending)
	}
	// Ses agents partent avec elle ; le jeton consommé reste, détaché.
	if agents := count(t, db, `SELECT COUNT(*) FROM agents WHERE machine_id = ?`, enrolled.ID); agents != 0 {
		t.Fatalf("%d agents survived their machine", agents)
	}
	if detached := count(t, db, `SELECT COUNT(*) FROM enrollment_tokens WHERE consumed_at IS NOT NULL AND agent_id IS NULL`); detached != 1 {
		t.Fatalf("%d consumed tokens detached", detached)
	}
}

func TestDeleteToken_OnlyWhilePending(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	cleartext := issueToken(t, db, "cancel-me", "", now)
	pending, err := db.ListPendingTokens(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteToken(ctx, pending[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteToken(ctx, pending[0].ID); !errors.Is(err, machine.ErrTokenNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if _, err := db.Enroll(ctx, machine.HashToken(cleartext), candidate(t), newAgent(t, now), now); !errors.Is(err, machine.ErrTokenNotFound) {
		t.Fatalf("cancelled token still enrolls: %v", err)
	}
}

func TestRecordConnection_UpdatesAddressVersionAndLastSeen(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	token := issueToken(t, db, "seen", "", now)
	enrolled, err := db.Enroll(ctx, machine.HashToken(token), candidate(t), newAgent(t, now), now)
	if err != nil {
		t.Fatal(err)
	}
	later := now.Add(time.Minute)
	connection := machine.Connection{MachineID: enrolled.ID, AgentID: enrolled.Agent.ID, Address: "51.15.20.114", Version: "v0.0.2", At: later}
	if err := db.RecordConnection(ctx, connection); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetMachine(ctx, enrolled.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Address != "51.15.20.114" || got.Agent.Version != "v0.0.2" || !got.LastSeenAt.Equal(later) || !got.Agent.LastSeenAt.Equal(later) {
		t.Fatalf("got %+v", got)
	}
	// Sans droits rapportés (un agent d'avant les droits), rien n'est écrit.
	if len(got.Agent.Capabilities) != 0 || !got.Agent.ReportedAt.IsZero() {
		t.Fatalf("capabilities %+v", got.Agent)
	}
	if err := db.RecordConnection(ctx, machine.Connection{MachineID: "nobody", At: later}); !errors.Is(err, machine.ErrNotFound) {
		t.Fatalf("unknown machine: %v", err)
	}
	// Le système redit à la connexion remplace celui de l'enrôlement.
	if err := db.RecordSystem(ctx, enrolled.ID, "Debian GNU/Linux 13"); err != nil {
		t.Fatal(err)
	}
	if got, err = db.GetMachine(ctx, enrolled.ID); err != nil || got.OS != "Debian GNU/Linux 13" || got.Address != "51.15.20.114" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if err := db.RecordSystem(ctx, "nobody", "Debian GNU/Linux 13"); !errors.Is(err, machine.ErrNotFound) {
		t.Fatalf("unknown machine: %v", err)
	}
}

// Une tâche rattachée retient sa machine (restrict) ; détachée, elle reste
// quand la machine part.
func TestDeleteMachine_IsHeldBackByItsHeartbeatsUntilDetached(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Now()
	cleartext := issueToken(t, db, "vps", "", now)
	enrolled, err := db.Enroll(ctx, machine.HashToken(cleartext), candidate(t), newAgent(t, now), now)
	if err != nil {
		t.Fatal(err)
	}
	monitor := heartbeat.Heartbeat{ID: "job1", Token: "hb_x", Name: "job", MachineID: enrolled.ID, Status: heartbeat.StatusNew, Interval: time.Hour, CreatedAt: now}
	if err := db.InsertHeartbeat(ctx, monitor); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteMachine(ctx, enrolled.ID); !errors.Is(err, machine.ErrHasHeartbeats) {
		t.Fatalf("attendu %v, obtenu %v", machine.ErrHasHeartbeats, err)
	}
	exec(t, db, `UPDATE heartbeats SET machine_id = NULL WHERE id = 'job1'`)
	if err := db.DeleteMachine(ctx, enrolled.ID); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetHeartbeat(ctx, "job1")
	if err != nil || got.MachineID != "" || got.MachineName != "" {
		t.Fatalf("heartbeat %+v err %v", got, err)
	}
}

func countRows(t *testing.T, db *DB, table string) int {
	t.Helper()
	var count int
	if err := db.sql.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func twoVolumes(at time.Time) sampler.Reading {
	return sampler.Reading{
		SampledAt: at, CPUCores: 2, MemTotal: 1, DiskUsed: 3, DiskTotal: 30,
		Disks: []sampler.Disk{{MountPoint: "/", Device: "/dev/vda1", Used: 1, Total: 10}, {MountPoint: "/data", Device: "/dev/vdb", Used: 2, Total: 20}},
	}
}

// Les volumes vivent avec le brut : la purge du brut les efface, le retrait
// de la machine aussi, et un échantillon rejoué ne les réécrit pas.
func TestPurgeSamples_AndDeleteMachine_TakeTheDisksAlong(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	cleartext := issueToken(t, db, "vps", "", now)
	enrolled, err := db.Enroll(ctx, machine.HashToken(cleartext), candidate(t), newAgent(t, now), now)
	if err != nil {
		t.Fatal(err)
	}
	old, fresh := twoVolumes(now.Add(-time.Hour)), twoVolumes(now)
	if err := db.InsertSamples(ctx, enrolled.ID, []sampler.Reading{old, fresh, fresh}); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, db, "machine_disks"); got != 4 {
		t.Fatalf("%d disk rows after insert, want 4", got)
	}
	if err := db.PurgeSamples(ctx, now.Add(-time.Minute), now, now); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, db, "machine_disks"); got != 2 {
		t.Fatalf("%d disk rows after purge, want 2", got)
	}
	if err := db.DeleteMachine(ctx, enrolled.ID); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, db, "machine_disks"); got != 0 {
		t.Fatalf("%d disk rows after delete, want 0", got)
	}
}

// Les droits rapportés sont un reflet du fichier de l'agent : chaque
// rapport remplace le précédent, et le jeton dit ce qui était demandé.
func TestRecordConnection_ReplacesTheReportedRights(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	requested, err := machine.ParseCapabilities("agent.update,files.write=/srv/workspace")
	if err != nil {
		t.Fatal(err)
	}
	cleartext, token, err := machine.NewToken("rights", "")
	if err != nil {
		t.Fatal(err)
	}
	token.Capabilities, token.CreatedAt, token.ExpiresAt = requested, now, now.Add(time.Hour)
	if err := db.InsertToken(ctx, token); err != nil {
		t.Fatal(err)
	}
	enrolled, err := db.Enroll(ctx, machine.HashToken(cleartext), candidate(t), newAgent(t, now), now)
	if err != nil {
		t.Fatal(err)
	}
	if !machine.SameCapabilities(enrolled.Agent.Requested, requested) {
		t.Fatalf("requested %+v", enrolled.Agent.Requested)
	}
	report := func(text string, at time.Time) machine.Machine {
		t.Helper()
		reported, err := machine.ParseCapabilities(text)
		if err != nil {
			t.Fatal(err)
		}
		connection := machine.Connection{MachineID: enrolled.ID, AgentID: enrolled.Agent.ID, Capabilities: reported, At: at}
		if err := db.RecordConnection(ctx, connection); err != nil {
			t.Fatal(err)
		}
		got, err := db.GetMachine(ctx, enrolled.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	first := report("observe,files.write=/srv/workspace:/opt/site", now.Add(time.Minute))
	if machine.FormatCapabilities(first.Agent.Capabilities) != "observe,files.write=/opt/site:/srv/workspace" || !first.Agent.CapabilitiesDiffer() {
		t.Fatalf("first report %+v", first.Agent)
	}
	second := report("agent.update,files.write=/srv/workspace", now.Add(2*time.Minute))
	if second.Agent.CapabilitiesDiffer() || !second.Agent.ReportedAt.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("second report %+v", second.Agent)
	}
	if rows := count(t, db, `SELECT COUNT(*) FROM agent_capabilities WHERE agent_id = ?`, enrolled.Agent.ID); rows != 3 {
		t.Fatalf("%d rows, the previous report must be replaced", rows)
	}
}

// Le compte et les raisons d'indisponibilité suivent le rapport : le compte se
// remplace s'il est redit et reste sinon, les raisons sont remplacées avec les
// droits (jamais gardées pour un droit que le rapport ne porte plus).
func TestRecordConnection_KeepsTheAccountAndReplacesTheReasons(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	token := issueToken(t, db, "account", "", now)
	enrolled, err := db.Enroll(ctx, machine.HashToken(token), candidate(t), newAgent(t, now), now)
	if err != nil {
		t.Fatal(err)
	}
	if enrolled.Agent.RunAs != nil || enrolled.Agent.Unavailable != nil {
		t.Fatalf("a fresh agent has said nothing: %+v", enrolled.Agent)
	}
	rights, err := machine.ParseCapabilities("agent.update,docker.deploy,backup.receive=/srv/backups")
	if err != nil {
		t.Fatal(err)
	}
	connect := func(connection machine.Connection, at time.Time) machine.Agent {
		t.Helper()
		connection.MachineID, connection.AgentID, connection.At = enrolled.ID, enrolled.Agent.ID, at
		if err := db.RecordConnection(ctx, connection); err != nil {
			t.Fatal(err)
		}
		got, err := db.GetMachine(ctx, enrolled.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got.Agent
	}
	first := connect(machine.Connection{
		Capabilities: rights, Account: &machine.Account{UID: 998, Name: "opencloud-agent"},
		Unavailable: map[string]string{machine.CapabilityDockerDeploy: "docker_denied", machine.CapabilityBackupReceive: "path_denied"},
	}, now.Add(time.Minute))
	if first.RunAs == nil || *first.RunAs != (machine.Account{UID: 998, Name: "opencloud-agent"}) {
		t.Fatalf("account %+v", first.RunAs)
	}
	if first.UnavailableReason(machine.CapabilityDockerDeploy) != "docker_denied" || first.UnavailableReason(machine.CapabilityBackupReceive) != "path_denied" ||
		first.UnavailableReason(machine.CapabilityAgentUpdate) != "" || first.UnavailableReason(machine.CapabilityObserve) != "" {
		t.Fatalf("reasons %+v", first.Unavailable)
	}
	// Le compte sans nom lisible s'écrit par son uid.
	if nameless := connect(machine.Connection{Account: &machine.Account{UID: 4294967295}}, now.Add(2*time.Minute)); nameless.RunAs == nil || nameless.RunAs.UID != 4294967295 || nameless.RunAs.Name != "" {
		t.Fatalf("nameless account %+v", nameless.RunAs)
	}
	// Un rapport de droits sans raison efface les raisons ; le compte, non dit,
	// reste.
	second := connect(machine.Connection{Capabilities: rights}, now.Add(3*time.Minute))
	if len(second.Unavailable) != 0 || second.RunAs == nil || second.RunAs.UID != 4294967295 {
		t.Fatalf("second report %+v / %+v", second.Unavailable, second.RunAs)
	}
	// Un agent d'avant, qui ne rapporte rien, ne touche ni droits ni raisons.
	connect(machine.Connection{Capabilities: rights, Unavailable: map[string]string{machine.CapabilityDockerDeploy: "docker_missing"}}, now.Add(4*time.Minute))
	silent := connect(machine.Connection{}, now.Add(5*time.Minute))
	if silent.UnavailableReason(machine.CapabilityDockerDeploy) != "docker_missing" || len(silent.Capabilities) != 4 {
		t.Fatalf("a connection without a report must leave the last one: %+v / %+v", silent.Unavailable, silent.Capabilities)
	}
	// Une raison pour un droit que le rapport ne porte pas n'a pas de ligne.
	onlyObserve, _ := machine.ParseCapabilities("")
	reported := connect(machine.Connection{Capabilities: onlyObserve, Unavailable: map[string]string{machine.CapabilityFilesWrite: "path_denied"}}, now.Add(6*time.Minute))
	if len(reported.Unavailable) != 0 {
		t.Fatalf("a reason for a right the report lacks was kept: %+v", reported.Unavailable)
	}
	// Le uid hors de ses bornes est refusé par la base elle-même.
	_, err = db.sql.ExecContext(ctx, `UPDATE agents SET run_as_uid = -1 WHERE id = ?`, enrolled.Agent.ID)
	if err == nil {
		t.Fatal("the database must refuse a negative uid")
	}
}

// Révoquer pose revoked_at sur l'agent actif et annule les jetons de
// ré-enrôlement en attente ; un jeton d'une autre machine reste.
func TestRevokeAgent_CancelsThePendingReenrollTokens(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	token := issueToken(t, db, "revoked", "", now)
	enrolled, err := db.Enroll(ctx, machine.HashToken(token), candidate(t), newAgent(t, now), now)
	if err != nil {
		t.Fatal(err)
	}
	issueToken(t, db, "revoked", enrolled.ID, now)
	issueToken(t, db, "other", "", now)

	revokedID, err := db.RevokeAgent(ctx, enrolled.ID, now.Add(time.Minute))
	if err != nil || revokedID != enrolled.Agent.ID {
		t.Fatalf("revoke: %q %v", revokedID, err)
	}
	pending, err := db.ListPendingTokens(ctx, now)
	if err != nil || len(pending) != 1 || pending[0].Name != "other" {
		t.Fatalf("pending after revoke: %+v %v", pending, err)
	}
	got, err := db.GetMachine(ctx, enrolled.ID)
	if err != nil || !got.Agent.IsRevoked() || got.Agent.ID != enrolled.Agent.ID {
		t.Fatalf("the revoked agent stays the current one: %+v %v", got.Agent, err)
	}
	if _, err := db.RevokeAgent(ctx, enrolled.ID, now); !errors.Is(err, machine.ErrNoAgent) {
		t.Fatalf("revoke twice: %v", err)
	}
}
