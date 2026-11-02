package alert_test

import (
	"context"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/restic"
)

func staleFinding(machineID string, since time.Time, danger bool) restic.Finding {
	return restic.Finding{
		Kind: restic.FindingStale, Danger: danger, ObjectID: "restic:b1", ObjectName: "nextcloud", MachineID: machineID, Since: since, Reason: "failed",
	}
}

// Les constats de la supervision ouvrent, aggravent puis reprennent leurs
// alertes : une par objet, le même type sous deux objets en fait deux, et ce
// que le passage suivant ne redit pas se résout.
func TestBackups_FindingsOpenAggravateAndResolve(t *testing.T) {
	f := newFixture(t)
	f.channel(t, "ops", alert.SeverityAttention, true)
	ctx := context.Background()
	f.enrollRemote(t)
	since := testNow.Add(-40 * time.Hour)

	f.BackupsJudged(ctx, []restic.Finding{
		staleFinding(remoteID, since, false),
		{Kind: restic.FindingSourceUnprotected, ObjectID: "source:vm:100", ObjectName: "docker1", MachineID: remoteID},
	})
	if f.kinds(t) != "backup_stale:attention,source_unprotected:attention" && f.kinds(t) != "source_unprotected:attention,backup_stale:attention" {
		t.Fatalf("kinds %q", f.kinds(t))
	}
	stale, err := f.db.GetOpenAlert(ctx, alert.KindBackupStale, alert.ObjectBackup, "restic:b1")
	if err != nil || stale.Details.Reason != "failed" || !stale.Details.Since.Equal(since) || stale.MachineID != remoteID {
		t.Fatalf("stale alert %+v %v", stale, err)
	}

	// Un second passage identique ne fait rien de plus ; la gravité qui monte
	// aggrave la même alerte.
	f.BackupsJudged(ctx, []restic.Finding{staleFinding(remoteID, since, false)})
	f.BackupsJudged(ctx, []restic.Finding{staleFinding(remoteID, since, true)})
	if f.kinds(t) != "backup_stale:danger" {
		t.Fatalf("after two passes: %q (the unprotected source is no longer reported, so it resolves)", f.kinds(t))
	}
	if got := f.sender.events(); got != "opened:ops,opened:ops,aggravated:ops,resolved:ops" && got != "opened:ops,opened:ops,resolved:ops,aggravated:ops" {
		t.Fatalf("events %q", got)
	}

	f.BackupsJudged(ctx, nil)
	if f.kinds(t) != "" {
		t.Fatalf("a finding that no longer comes is resolved: %q", f.kinds(t))
	}
}

func TestBackups_AnAlertGoesWithItsMachine(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	enrolled := f.enrollRemote(t)
	f.BackupsJudged(ctx, []restic.Finding{{Kind: restic.FindingTargetUnreachable, Danger: true, ObjectID: "target:" + remoteID, ObjectName: "nas", MachineID: remoteID, Reason: restic.ReasonTargetOffline, Count: 2}})
	if f.kinds(t) != "target_unreachable:danger" {
		t.Fatalf("kinds %q", f.kinds(t))
	}
	if err := f.machines.Remove(ctx, enrolled.ID, machine.RemoveOptions{}); err != nil {
		t.Fatal(err)
	}
	if f.kinds(t) != "" {
		t.Fatalf("the machine took its backup alerts with it: %q", f.kinds(t))
	}
}

// Un silence sur la machine tait l'alerte de sauvegarde qu'elle porte.
func TestBackups_AMachineMuteSilencesItsBackupAlerts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.enrollRemote(t)
	f.mute(t, alert.MuteDefinition{Target: alert.Target{Kind: alert.TargetMachine, ID: remoteID}, Duration: time.Hour})
	f.BackupsJudged(ctx, []restic.Finding{staleFinding(remoteID, testNow.Add(-40*time.Hour), false)})
	open := f.only(t, alert.StatusOpen)
	if !open.Muted {
		t.Fatalf("alert not muted: %+v", open)
	}
}
