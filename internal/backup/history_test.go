package backup_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/backup"
)

// Chaque cible d'un passage laisse sa ligne : reçue avec la taille de son
// archive, en échec avec un mot fermé, refusée quand rien n'est parti. Les
// comptes du plan se lisent dans ces lignes.
func TestHistory_EveryTargetOfAPassLeavesItsLine(t *testing.T) {
	b := newBench(t, rights)
	destination := b.readyAgent("100,101,102", 3)
	fleet := playAgents(t, b)
	fleet.failProduce["100"] = agentwire.ReasonProducerFailed
	fleet.refuseReceive["101"] = agentwire.ReasonDiskFull

	if _, err := b.backups.RunNow(operator(), "pve"); err != nil {
		t.Fatal(err)
	}
	b.backups.Settle()

	runs, err := b.backups.Runs(context.Background(), backup.RunQuery{Subject: "pve"})
	if err != nil || len(runs) != 3 {
		t.Fatalf("runs %+v %v", runs, err)
	}
	// Les plus récentes d'abord : 102, 101, 100.
	received, refused, failed := runs[0], runs[1], runs[2]
	if received.Source != backup.GuestSourceKey(sourceID, 102) || received.Result != backup.ResultOK || received.Reason != "" ||
		received.SizeBytes == nil || *received.SizeBytes <= 0 {
		t.Fatalf("received %+v", received)
	}
	if received.DestinationID != destination.ID || received.DestinationName != destination.Name || received.ActionRunID == "" {
		t.Fatalf("destination of the line %+v", received)
	}
	if !received.PassAt.Equal(benchNow) || received.FinishedAt.Before(received.StartedAt) {
		t.Fatalf("times %+v", received)
	}
	if refused.Result != backup.ResultFailed || refused.Reason != agentwire.ReasonDiskFull || refused.SizeBytes != nil {
		t.Fatalf("destination refusal %+v", refused)
	}
	if failed.Source != backup.GuestSourceKey(sourceID, 100) || failed.Result != backup.ResultFailed || failed.Reason != agentwire.ReasonProducerFailed {
		t.Fatalf("producer failure %+v", failed)
	}

	only, err := b.backups.Runs(context.Background(), backup.RunQuery{Source: backup.GuestSourceKey(sourceID, 101)})
	if err != nil || len(only) != 1 || only[0].Reason != agentwire.ReasonDiskFull {
		t.Fatalf("filtered by source %+v %v", only, err)
	}
	overview, _ := b.backups.Overview(context.Background())
	if pve := overview.Plans[1]; pve.LastTargetsOK != 1 || pve.LastTargetsFailed != 2 {
		t.Fatalf("counts %d ok, %d failed", pve.LastTargetsOK, pve.LastTargetsFailed)
	}
}

// Un refus avant tout ordre s'écrit dans l'historique, avec la clé du refus :
// pour la base comme pour le plan entier d'une machine.
func TestHistory_ARefusalBeforeAnyOrderIsRecorded(t *testing.T) {
	b := newBench(t, rights)
	destination := b.readyAgent("100", 3)
	fleet := playAgents(t, b)
	b.machines.Disconnect(b.session)

	if _, err := b.backups.RunNow(operator(), "pve"); !errors.Is(err, backup.ErrMachineOffline) {
		t.Fatalf("agent plan: %v", err)
	}
	if _, err := b.backups.RunNow(operator(), backup.SubjectDatabase); !errors.Is(err, backup.ErrMachineOffline) {
		t.Fatalf("database: %v", err)
	}
	if len(fleet.steps) != 0 {
		t.Fatalf("orders were sent: %v", fleet.steps)
	}

	runs, err := b.backups.Runs(context.Background(), backup.RunQuery{})
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs %+v %v", runs, err)
	}
	database, whole := runs[0], runs[1]
	if database.Subject != backup.SubjectDatabase || database.Source != backup.SourceDatabaseKey || database.Result != backup.ResultRefused || database.Reason != "backup.machine_offline" {
		t.Fatalf("database %+v", database)
	}
	if whole.Subject != "pve" || whole.Source != "" || whole.Result != backup.ResultRefused || whole.DestinationID != destination.ID {
		t.Fatalf("plan %+v", whole)
	}
	// Un plan refusé en entier n'a pas de cibles à compter.
	overview, _ := b.backups.Overview(context.Background())
	if pve := overview.Plans[1]; pve.LastTargetsOK != 0 || pve.LastTargetsFailed != 0 {
		t.Fatalf("counts %d ok, %d failed", pve.LastTargetsOK, pve.LastTargetsFailed)
	}
}

// La base est reçue quand son ordre backup.receive réussit, pas quand il part :
// la ligne attend la réponse de l'agent.
func TestHistory_TheDatabaseLineWaitsForTheReceive(t *testing.T) {
	b := newBench(t, rights)
	b.ready()
	playAgents(t, b)

	if _, err := b.backups.RunNow(operator(), backup.SubjectDatabase); err != nil {
		t.Fatal(err)
	}
	b.backups.Settle()

	runs, err := b.backups.Runs(context.Background(), backup.RunQuery{Subject: backup.SubjectDatabase})
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs %+v %v", runs, err)
	}
	if run := runs[0]; run.Source != backup.SourceDatabaseKey || run.Result != backup.ResultOK || run.SizeBytes == nil || *run.SizeBytes <= 0 || run.DestinationName != "store-1" {
		t.Fatalf("run %+v", run)
	}
}
