package store

import (
	"context"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/deploy"
)

const (
	updateServiceID = "svc_00000000000000a1"
	updateMachineID = "m1"
)

func openWithService(t *testing.T) *DB {
	t.Helper()
	db := openTestDB(t)
	exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES ('m1', 'vps-1', 1)`)
	exec(t, db, `INSERT INTO declared_services (id, name, machine_id, nature, origin, declared_by, created_at, updated_at)
		VALUES ('svc_00000000000000a1', 'nextcloud', 'm1', 'application', 'compose', 'operator', 1, 1)`)
	return db
}

func pendingUpdate(runID string, at time.Time) deploy.ServiceUpdate {
	return deploy.ServiceUpdate{
		RunID: runID, ServiceID: updateServiceID, MachineID: updateMachineID, UnitID: "unt_1", UnitName: "app",
		FromVersion: "29.0.4", ToVersion: "29.0.5", State: deploy.UpdatePending, RequestedAt: at,
	}
}

// La 039 sur une base existante : un agent déjà enrôlé n'a pas le retour d'un
// service (0), un canal déjà posé joint ses journaux (1, le défaut).
func TestMigration039_KeepsExistingChannelsAndAgents(t *testing.T) {
	ctx := context.Background()
	db := openEmpty(t)
	names, err := migrationFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if version := migrationVersion(name); version < 39 {
			if err := db.apply(ctx, name, version); err != nil {
				t.Fatal(err)
			}
		}
	}
	exec(t, db, `INSERT INTO alert_channels (name, url, format, min_severity, created_at) VALUES ('ntfy', '', 'text', 'attention', 1)`)
	exec(t, db, `INSERT INTO machines (id, name, created_at) VALUES ('m1', 'vps-1', 1)`)
	exec(t, db, `INSERT INTO agents (id, machine_id, public_key, enrolled_at) VALUES ('agt_1', 'm1', zeroblob(32), 1)`)

	if err := db.apply(ctx, "039_service_updates.sql", 39); err != nil {
		t.Fatal(err)
	}
	if got := count(t, db, `SELECT attach_logs FROM alert_channels WHERE name = 'ntfy'`); got != 1 {
		t.Errorf("attach_logs of an existing channel: %d", got)
	}
	if got := count(t, db, `SELECT service_rollback_version FROM agents WHERE id = 'agt_1'`); got != 0 {
		t.Errorf("service_rollback_version of an existing agent: %d", got)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM service_updates`); got != 0 {
		t.Errorf("service_updates not empty: %d", got)
	}
}

func TestServiceUpdates_AreKeptSettledOnceAndFoundByMachineAndService(t *testing.T) {
	ctx := context.Background()
	db := openWithService(t)
	now := time.Unix(1_800_000_000, 0)
	if err := db.InsertServiceUpdate(ctx, pendingUpdate("run1", now)); err != nil {
		t.Fatal(err)
	}

	if _, found, err := db.FindPendingServiceUpdate(ctx, updateMachineID, now.Add(-time.Hour)); err != nil || !found {
		t.Fatalf("pending on the machine: %v %v", found, err)
	}
	if _, found, _ := db.FindPendingServiceUpdate(ctx, updateMachineID, now.Add(time.Minute)); found {
		t.Error("a pending update older than the window still blocks the machine")
	}
	if _, found, _ := db.FindPendingServiceUpdate(ctx, "m2", now.Add(-time.Hour)); found {
		t.Error("another machine is blocked")
	}

	settled, found, err := db.SettleServiceUpdate(ctx, "run1", deploy.UpdateRolledBack, "rolled_back_version", now.Add(time.Minute))
	if err != nil || !found || settled.State != deploy.UpdateRolledBack || settled.Reason != "rolled_back_version" || settled.UnitID != "unt_1" || settled.SettledAt.IsZero() {
		t.Fatalf("settled %+v %v %v", settled, found, err)
	}
	// Fermée une fois : un rapport rejoué, ou un autre run, ne la rouvre pas.
	if _, again, _ := db.SettleServiceUpdate(ctx, "run1", deploy.UpdateConfirmed, "", now); again {
		t.Error("a settled update settled twice")
	}
	if _, unknown, _ := db.SettleServiceUpdate(ctx, "run404", deploy.UpdateConfirmed, "", now); unknown {
		t.Error("an unknown run settled")
	}
	if _, found, _ := db.FindPendingServiceUpdate(ctx, updateMachineID, now.Add(-time.Hour)); found {
		t.Error("a settled update still blocks the machine")
	}

	if err := db.InsertServiceUpdate(ctx, pendingUpdate("run2", now.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	latest, found, err := db.LatestServiceUpdate(ctx, updateServiceID)
	if err != nil || !found || latest.RunID != "run2" {
		t.Fatalf("latest %+v %v %v", latest, found, err)
	}
	if _, found, _ := db.LatestServiceUpdate(ctx, "svc_none"); found {
		t.Error("a service with no change has a latest one")
	}
}

// Supprimer le service emporte ses montées : la ligne n'a plus de sens.
func TestServiceUpdates_GoWithTheirService(t *testing.T) {
	ctx := context.Background()
	db := openWithService(t)
	if err := db.InsertServiceUpdate(ctx, pendingUpdate("run1", time.Unix(1_800_000_000, 0))); err != nil {
		t.Fatal(err)
	}
	exec(t, db, `DELETE FROM declared_services WHERE id = 'svc_00000000000000a1'`)
	if got := count(t, db, `SELECT COUNT(*) FROM service_updates`); got != 0 {
		t.Fatalf("%d updates left", got)
	}
}

func TestChannels_KeepTheirAttachLogsChoice(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	now := time.Unix(1_800_000_000, 0)
	id, err := db.InsertChannel(ctx, alert.Channel{Name: "ntfy", URL: plainChannelURL, Format: alert.FormatText, MinSeverity: alert.SeverityAttention, AttachLogs: false, Enabled: true, CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := db.GetChannel(ctx, id)
	if err != nil || channel.AttachLogs {
		t.Fatalf("channel %+v %v", channel, err)
	}
	channel.AttachLogs = true
	if err := db.UpdateChannel(ctx, channel); err != nil {
		t.Fatal(err)
	}
	if again, err := db.GetChannel(ctx, id); err != nil || !again.AttachLogs {
		t.Fatalf("after update: %+v %v", again, err)
	}
}
