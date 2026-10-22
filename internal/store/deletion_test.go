package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/deletion"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/status"
)

// deletionBench pose la machine openCloud, une machine distante « m1 » et un
// service déclaré sur la machine openCloud (svc_0123456789abcdef) : de quoi
// poser, test par test, un lien entre objets déclarés.
func deletionBench(t *testing.T) *DB {
	t.Helper()
	db := openTestDB(t)
	declaredOnLocal(t, db)
	exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES ('m1', 'vps-paris-1', 1)`)
	return db
}

func insertTestProbe(t *testing.T, db *DB, id, machineID, serviceID string) {
	t.Helper()
	err := db.InsertProbe(context.Background(), probe.Probe{
		ID: id, Name: "site-" + id, Kind: probe.KindHTTP, Target: "https://a.fr/", MachineID: machineID, ServiceID: serviceID,
		Status: probe.StatusNew, Interval: time.Minute, Timeout: 10 * time.Second,
		FailureThreshold: 3, RecoveryThreshold: 2, Method: "GET", ExpectedStatus: "2xx", CreatedAt: linksNow,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func insertTestHeartbeat(t *testing.T, db *DB, id, machineID string) {
	t.Helper()
	err := db.InsertHeartbeat(context.Background(), heartbeat.Heartbeat{
		ID: id, Token: "hb_" + id, Name: "job-" + id, MachineID: machineID, Status: heartbeat.StatusNew, Interval: time.Hour, CreatedAt: linksNow,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func insertTestComponent(t *testing.T, db *DB, id string, members ...status.Member) {
	t.Helper()
	if err := db.InsertComponent(context.Background(), status.Component{ID: id, PageID: insertTestPage(t, db, "statut"), Name: "Composant " + id, CreatedAt: linksNow, Members: members}); err != nil {
		t.Fatal(err)
	}
}

// Entre objets déclarés, chaque clé étrangère est en restrict : un DELETE
// brut du parent est refusé tant que le lien est là, et passe sans lui. C'est
// le filet sous le plan, même pour qui écrirait sans le composant.
func TestRestrict_EveryLinkBetweenDeclaredObjectsRefusesARawDelete(t *testing.T) {
	const (
		deleteRemote  = `DELETE FROM machines WHERE id = 'm1'`
		deleteService = `DELETE FROM declared_services WHERE id = 'svc_0123456789abcdef'`
	)
	cases := []struct {
		name   string
		link   func(t *testing.T, db *DB)
		unlink string
		parent string
	}{
		{
			name: "declared_services.machine_id",
			link: func(t *testing.T, db *DB) {
				exec(t, db, `INSERT INTO declared_services (id, name, machine_id, nature, origin, declared_by, created_at, updated_at)
					VALUES ('svc_00000000000000a1', 'wiki', 'm1', 'application', 'container', 'operator', 1, 1)`)
			},
			unlink: `DELETE FROM declared_services WHERE id = 'svc_00000000000000a1'`,
			parent: deleteRemote,
		},
		{
			name:   "probes.machine_id",
			link:   func(t *testing.T, db *DB) { insertTestProbe(t, db, "p1", "m1", "") },
			unlink: `DELETE FROM probes WHERE id = 'p1'`,
			parent: deleteRemote,
		},
		{
			name:   "probes.service_id",
			link:   func(t *testing.T, db *DB) { insertTestProbe(t, db, "p1", machine.LocalID, "svc_0123456789abcdef") },
			unlink: `DELETE FROM probes WHERE id = 'p1'`,
			parent: deleteService,
		},
		{
			name:   "heartbeats.machine_id",
			link:   func(t *testing.T, db *DB) { insertTestHeartbeat(t, db, "h1", "m1") },
			unlink: `DELETE FROM heartbeats WHERE id = 'h1'`,
			parent: deleteRemote,
		},
		{
			name: "status_component_members.machine_id",
			link: func(t *testing.T, db *DB) {
				insertTestComponent(t, db, "c1", status.Member{Kind: status.KindMachine, ID: "m1"})
			},
			unlink: `DELETE FROM status_components WHERE id = 'c1'`,
			parent: deleteRemote,
		},
		{
			name: "status_component_members.service_id",
			link: func(t *testing.T, db *DB) {
				insertTestComponent(t, db, "c1", status.Member{Kind: status.KindService, ID: "svc_0123456789abcdef"})
			},
			unlink: `DELETE FROM status_components WHERE id = 'c1'`,
			parent: deleteService,
		},
		{
			name: "status_component_members.heartbeat_id",
			link: func(t *testing.T, db *DB) {
				insertTestHeartbeat(t, db, "h1", "")
				insertTestComponent(t, db, "c1", status.Member{Kind: status.KindHeartbeat, ID: "h1"})
			},
			unlink: `DELETE FROM status_components WHERE id = 'c1'`,
			parent: `DELETE FROM heartbeats WHERE id = 'h1'`,
		},
		{
			name: "status_component_members.probe_id",
			link: func(t *testing.T, db *DB) {
				insertTestProbe(t, db, "p1", machine.LocalID, "")
				insertTestComponent(t, db, "c1", status.Member{Kind: status.KindProbe, ID: "p1"})
			},
			unlink: `DELETE FROM status_components WHERE id = 'c1'`,
			parent: `DELETE FROM probes WHERE id = 'p1'`,
		},
	}
	for _, link := range cases {
		t.Run(link.name, func(t *testing.T) {
			db := deletionBench(t)
			link.link(t, db)
			if _, err := db.sql.Exec(link.parent); err == nil {
				t.Fatal("la clé étrangère doit refuser la suppression du parent")
			}
			exec(t, db, link.unlink)
			exec(t, db, link.parent)
		})
	}
}

// Ce qu'une machine a observé et mesuré part avec elle (cascade), compté
// d'avance par son plan ; ses alertes restent, détachées, avec leur nom, et
// les incidents qui la visaient gardent leur texte.
func TestDeleteMachine_TakesWhatItOwnsAndDetachesItsAlerts(t *testing.T) {
	ctx := context.Background()
	db := deletionBench(t)
	exec(t, db, `INSERT INTO enrollment_tokens (id, token_hash, token_prefix, name, machine_id, created_at, expires_at)
		VALUES ('tok1', 'hash', 'oc_', 'vps-paris-1', 'm1', 1, 9999999999)`)
	exec(t, db, `INSERT INTO agents (id, machine_id, public_key, enrolled_at, revoked_at) VALUES ('agt_1', 'm1', zeroblob(32), 1, 2)`)
	exec(t, db, `INSERT INTO agents (id, machine_id, public_key, enrolled_at) VALUES ('agt_2', 'm1', zeroblob(32), 2)`)
	exec(t, db, `INSERT INTO agent_capabilities (agent_id, capability, reported_at) VALUES ('agt_2', 'observe', 2)`)
	exec(t, db, `INSERT INTO machine_samples (machine_id, sampled_at, cpu_percent, cpu_cores, load_1, mem_used, mem_total,
		swap_used, swap_total, disk_used, disk_total, net_rx_per_second, net_tx_per_second) VALUES ('m1', 1, 0, 1, 0, 0, 1, 0, 0, 0, 1, 0, 0)`)
	exec(t, db, `INSERT INTO machine_rollups (machine_id, granularity, bucket, avg_cpu_percent, cpu_cores, avg_load_1, avg_mem_used, avg_mem_total,
		avg_swap_used, avg_swap_total, avg_disk_used, avg_disk_total, avg_net_rx_per_second, avg_net_tx_per_second, sample_count)
		VALUES ('m1', 'hour', 1, 0, 1, 0, 0, 1, 0, 0, 0, 1, 0, 0, 1)`)
	exec(t, db, `INSERT INTO machine_networks (machine_id, network_id, name, driver, seen_at) VALUES ('m1', 'n1', 'bridge', 'bridge', 1)`)
	exec(t, db, `INSERT INTO containers (id, machine_id, name, container_id, state, created_at, first_seen_at, last_seen_at)
		VALUES ('m1:abc', 'm1', 'wiki', 'abc', 'running', 1, 1, 1)`)
	exec(t, db, `INSERT INTO container_events (container_id, at, action) VALUES ('m1:abc', 1, 'start'), ('m1:abc', 2, 'die')`)
	exec(t, db, `INSERT INTO image_checks (machine_id, image, checked_at, outcome) VALUES ('m1', 'nginx', 1, 'ok')`)
	exec(t, db, `INSERT INTO ignored_keys (machine_id, kind, value, created_at) VALUES ('m1', 'container_name', 'init', 1)`)
	// Une fenêtre sur la machine, une sur son conteneur : les deux partent.
	exec(t, db, `INSERT INTO mutes (machine_id, starts_at, ends_at, created_at) VALUES ('m1', 1, 2, 1)`)
	exec(t, db, `INSERT INTO mutes (container_id, starts_at, ends_at, created_at) VALUES ('m1:abc', 1, 2, 1)`)
	exec(t, db, `INSERT INTO mutes (alert_kind, starts_at, ends_at, created_at) VALUES ('disk_full', 1, 2, 1)`)
	exec(t, db, `INSERT INTO alerts (kind, severity, status, object_kind, object_id, object_name, machine_id, opened_at, updated_at)
		VALUES ('machine_lost', 'danger', 'resolved', 'machine', 'm1', 'vps-paris-1', 'm1', 1, 1)`)
	insertTestIncident(t, db, "i1", "machine_id", "m1")

	plan, err := db.MachineDeletionPlan(ctx, "m1")
	if err != nil {
		t.Fatal(err)
	}
	want := deletion.Plan{
		Removes: []deletion.Count{
			{Kind: deletion.KindAgents, Count: 2},
			{Kind: deletion.KindEnrollmentTokens, Count: 1}, {Kind: deletion.KindMachineSamples, Count: 2},
			{Kind: deletion.KindMachineNetworks, Count: 1}, {Kind: deletion.KindContainers, Count: 1},
			{Kind: deletion.KindContainerEvents, Count: 2}, {Kind: deletion.KindImageChecks, Count: 1},
			{Kind: deletion.KindIgnoredKeys, Count: 1}, {Kind: deletion.KindSilences, Count: 2},
		},
		Detaches: []deletion.Count{{Kind: deletion.KindAlerts, Count: 1}, {Kind: deletion.KindIncidentLinks, Count: 1}},
	}
	if !reflect.DeepEqual(plan, want) {
		t.Fatalf("plan = %+v", plan)
	}

	if err := db.DeleteMachine(ctx, "m1"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"agents", "enrollment_tokens", "machine_samples", "machine_rollups", "machine_networks", "containers", "image_checks", "ignored_keys"} {
		if got := count(t, db, `SELECT COUNT(*) FROM `+table+` WHERE machine_id = 'm1'`); got != 0 {
			t.Errorf("%s : %d ligne(s) survivent à la machine", table, got)
		}
	}
	if got := count(t, db, `SELECT COUNT(*) FROM container_events`); got != 0 {
		t.Errorf("container_events : %d ligne(s) survivent", got)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM agent_capabilities`); got != 0 {
		t.Errorf("agent_capabilities : %d ligne(s) survivent", got)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM mutes`); got != 1 {
		t.Errorf("mutes : %d, seule la fenêtre sans cible doit rester", got)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM alerts WHERE machine_id IS NULL AND object_name = 'vps-paris-1'`); got != 1 {
		t.Fatalf("l'alerte doit rester, détachée et nommée : %d", got)
	}
	requireIncidentKeptWithoutTargets(t, db, "i1")
}

// insertTestIncident pose un incident ouvert qui vise l'objet donné, par sa
// colonne de incident_targets.
func insertTestIncident(t *testing.T, db *DB, id, column, objectID string) {
	t.Helper()
	exec(t, db, `INSERT INTO incidents (id, title, impact, status, created_at, updated_at) VALUES (?, 'Panne', 'down', 'investigating', 1, 1)`, id)
	exec(t, db, `INSERT INTO incident_targets (incident_id, `+column+`) VALUES (?, ?)`, id, objectID)
}

// Un objet retiré emporte le lien, jamais l'incident.
func requireIncidentKeptWithoutTargets(t *testing.T, db *DB, id string) {
	t.Helper()
	if got := count(t, db, `SELECT COUNT(*) FROM incidents WHERE id = ?`, id); got != 1 {
		t.Fatal("l'incident doit rester")
	}
	if got := count(t, db, `SELECT COUNT(*) FROM incident_targets WHERE incident_id = ?`, id); got != 0 {
		t.Fatalf("le lien doit partir avec l'objet : %d", got)
	}
}

// Une machine que des objets déclarés visent est refusée avec la clé du
// premier genre qui la retient et le plan entier ; rien ne part.
func TestDeleteMachine_RefusesWithTheKeyOfTheFirstBlocker(t *testing.T) {
	ctx := context.Background()
	db := deletionBench(t)
	insertTestHeartbeat(t, db, "h1", "m1")
	insertTestComponent(t, db, "c1", status.Member{Kind: status.KindMachine, ID: "m1"})

	err := db.DeleteMachine(ctx, "m1")

	var refused *deletion.RefusedError
	if !errors.Is(err, machine.ErrHasHeartbeats) || !errors.As(err, &refused) {
		t.Fatalf("attendu machine.has_heartbeats avec le plan, obtenu %v", err)
	}
	want := []deletion.Blocker{
		{Kind: deletion.KindHeartbeats, Objects: []deletion.Object{{ID: "h1", Name: "job-h1"}}},
		{Kind: deletion.KindComponents, Objects: []deletion.Object{{ID: "c1", Name: "Composant c1"}}},
	}
	if !reflect.DeepEqual(refused.Plan.Blockers, want) {
		t.Fatalf("bloqueurs = %+v", refused.Plan.Blockers)
	}
	if _, err := db.GetMachine(ctx, "m1"); err != nil {
		t.Fatalf("la machine a disparu malgré le refus: %v", err)
	}
}

// Une sonde emporte son historique et ses silences ; ses alertes restent.
// Affichée par un composant, elle est retenue.
func TestDeleteProbe_TakesItsHistoryUnlessAComponentShowsIt(t *testing.T) {
	ctx := context.Background()
	db := deletionBench(t)
	insertTestProbe(t, db, "p1", "m1", "")
	exec(t, db, `INSERT INTO probe_results (probe_id, checked_at, outcome, duration_ms) VALUES ('p1', 1, 'up', 5), ('p1', 2, 'down', 5)`)
	exec(t, db, `INSERT INTO probe_days (probe_id, day, total, success, degraded, duration_ms) VALUES ('p1', 1, 2, 1, 0, 10)`)
	exec(t, db, `INSERT INTO alerts (kind, severity, status, object_kind, object_id, object_name, probe_id, opened_at, updated_at)
		VALUES ('probe_down', 'danger', 'resolved', 'probe', 'p1', 'site-p1', 'p1', 1, 1)`)
	insertTestComponent(t, db, "c1", status.Member{Kind: status.KindProbe, ID: "p1"})
	insertTestIncident(t, db, "i1", "probe_id", "p1")

	if err := db.DeleteProbe(ctx, "p1"); !errors.Is(err, probe.ErrShownOnStatus) {
		t.Fatalf("attendu %v, obtenu %v", probe.ErrShownOnStatus, err)
	}
	exec(t, db, `DELETE FROM status_components WHERE id = 'c1'`)
	plan, err := db.ProbeDeletionPlan(ctx, "p1")
	want := deletion.Plan{
		Removes:  []deletion.Count{{Kind: deletion.KindProbeResults, Count: 2}, {Kind: deletion.KindProbeDays, Count: 1}},
		Detaches: []deletion.Count{{Kind: deletion.KindAlerts, Count: 1}, {Kind: deletion.KindIncidentLinks, Count: 1}},
	}
	if err != nil || !reflect.DeepEqual(plan, want) {
		t.Fatalf("plan = %+v %v", plan, err)
	}
	if err := db.DeleteProbe(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM probe_results`) + count(t, db, `SELECT COUNT(*) FROM probe_days`); got != 0 {
		t.Fatalf("l'historique doit partir avec la sonde : %d ligne(s)", got)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM alerts WHERE probe_id IS NULL`); got != 1 {
		t.Fatalf("l'alerte doit rester, détachée : %d", got)
	}
	requireIncidentKeptWithoutTargets(t, db, "i1")
	if err := db.DeleteProbe(ctx, "p1"); !errors.Is(err, probe.ErrNotFound) {
		t.Fatalf("supprimée deux fois: %v", err)
	}
}

// Une tâche emporte ses pings et ses exécutions ; affichée par un composant,
// elle est retenue.
func TestDeleteHeartbeat_TakesItsHistoryUnlessAComponentShowsIt(t *testing.T) {
	ctx := context.Background()
	db := deletionBench(t)
	insertTestHeartbeat(t, db, "h1", "")
	exec(t, db, `INSERT INTO heartbeat_pings (heartbeat_id, kind, received_at) VALUES ('h1', 'finish', 1)`)
	exec(t, db, `INSERT INTO heartbeat_runs (heartbeat_id, outcome) VALUES ('h1', 'success'), ('h1', 'failure')`)
	insertTestComponent(t, db, "c1", status.Member{Kind: status.KindHeartbeat, ID: "h1"})
	insertTestIncident(t, db, "i1", "heartbeat_id", "h1")

	if err := db.DeleteHeartbeat(ctx, "h1"); !errors.Is(err, heartbeat.ErrShownOnStatus) {
		t.Fatalf("attendu %v, obtenu %v", heartbeat.ErrShownOnStatus, err)
	}
	exec(t, db, `DELETE FROM status_components WHERE id = 'c1'`)
	plan, err := db.HeartbeatDeletionPlan(ctx, "h1")
	want := deletion.Plan{
		Removes:  []deletion.Count{{Kind: deletion.KindHeartbeatPings, Count: 1}, {Kind: deletion.KindHeartbeatRuns, Count: 2}},
		Detaches: []deletion.Count{{Kind: deletion.KindIncidentLinks, Count: 1}},
	}
	if err != nil || !reflect.DeepEqual(plan, want) {
		t.Fatalf("plan = %+v %v", plan, err)
	}
	if err := db.DeleteHeartbeat(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	requireIncidentKeptWithoutTargets(t, db, "i1")
	if got := count(t, db, `SELECT COUNT(*) FROM heartbeat_pings`) + count(t, db, `SELECT COUNT(*) FROM heartbeat_runs`); got != 0 {
		t.Fatalf("l'historique doit partir avec la tâche : %d ligne(s)", got)
	}
}

// Un service emporte ses unités, ses fenêtres pour taire et ses liens de
// projet, ses conteneurs redeviennent non déclarés et ses alertes restent,
// détachées ; un projet ne retire que ses liens ; un composant emporte ses
// membres sans toucher aux incidents ; l'incident qui visait le service reste,
// détaché.
func TestDeletionPlans_OfServiceProjectAndComponentSayWhatGoesAndWhatStays(t *testing.T) {
	ctx := context.Background()
	db := deletionBench(t)
	exec(t, db, `INSERT INTO projects (id, name, title, created_at, updated_at) VALUES ('prj_0000000000000001', 'docs', '', 1, 1)`)
	exec(t, db, `INSERT INTO project_services (project_id, service_id) VALUES ('prj_0000000000000001', 'svc_0123456789abcdef')`)
	exec(t, db, `INSERT INTO containers (id, machine_id, name, container_id, state, created_at, first_seen_at, last_seen_at, unit_id)
		VALUES ('local:abc', 'local', 'whoami', 'abc', 'running', 1, 1, 1, 'unit_0123456789abcdef')`)
	exec(t, db, `INSERT INTO mutes (service_id, starts_at, ends_at, created_at) VALUES ('svc_0123456789abcdef', 1, 2, 1)`)
	exec(t, db, `INSERT INTO alerts (kind, severity, status, object_kind, object_id, object_name, machine_id, service_id, container_id, opened_at, updated_at)
		VALUES ('service_down', 'danger', 'open', 'container', 'local:abc', 'whoami', 'local', 'svc_0123456789abcdef', 'local:abc', 1, 1)`)
	insertTestComponent(t, db, "c1", status.Member{Kind: status.KindMachine, ID: "m1"})
	insertTestIncident(t, db, "i1", "service_id", "svc_0123456789abcdef")

	service, err := db.DeclaredServiceDeletionPlan(ctx, "svc_0123456789abcdef")
	wantService := deletion.Plan{
		Removes: []deletion.Count{{Kind: deletion.KindUnits, Count: 1}, {Kind: deletion.KindSilences, Count: 1}},
		Detaches: []deletion.Count{
			{Kind: deletion.KindProjectLinks, Count: 1}, {Kind: deletion.KindContainers, Count: 1}, {Kind: deletion.KindAlerts, Count: 1},
			{Kind: deletion.KindIncidentLinks, Count: 1},
		},
	}
	if err != nil || !reflect.DeepEqual(service, wantService) {
		t.Fatalf("service = %+v %v", service, err)
	}
	project, err := db.ProjectDeletionPlan(ctx, "prj_0000000000000001")
	if err != nil || !reflect.DeepEqual(project, deletion.Plan{Detaches: []deletion.Count{{Kind: deletion.KindProjectLinks, Count: 1}}}) {
		t.Fatalf("projet = %+v %v", project, err)
	}
	component, err := db.ComponentDeletionPlan(ctx, "c1")
	wantComponent := deletion.Plan{Removes: []deletion.Count{{Kind: deletion.KindMembers, Count: 1}}}
	if err != nil || !reflect.DeepEqual(component, wantComponent) {
		t.Fatalf("composant = %+v %v", component, err)
	}

	if err := db.DeleteDeclaredService(ctx, "svc_0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM containers WHERE id = 'local:abc' AND unit_id IS NULL`); got != 1 {
		t.Fatal("le conteneur doit rester, non déclaré")
	}
	if got := count(t, db, `SELECT COUNT(*) FROM alerts WHERE service_id IS NULL AND container_id = 'local:abc' AND status = 'open'`); got != 1 {
		t.Fatal("l'alerte du conteneur doit rester ouverte, détachée du service")
	}
	if got := count(t, db, `SELECT COUNT(*) FROM mutes`); got != 0 {
		t.Fatal("la fenêtre pour taire doit partir avec le service")
	}
	if _, err := db.GetProject(ctx, "prj_0000000000000001"); err != nil {
		t.Fatalf("le projet doit rester: %v", err)
	}
	requireIncidentKeptWithoutTargets(t, db, "i1")
	if err := db.DeleteComponent(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM status_component_members`); got != 0 {
		t.Fatalf("les membres partent avec le composant : %d", got)
	}
	if _, err := db.GetDeclaredService(ctx, "svc_0123456789abcdef"); !errors.Is(err, directory.ErrNotFound) {
		t.Fatalf("service supprimé: %v", err)
	}
}
