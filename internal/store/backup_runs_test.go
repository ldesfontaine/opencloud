package store

import (
	"context"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/backup"
)

func runRecord(subject string, number int, result backup.Result) backup.RunRecord {
	at := backupsNow.Add(time.Duration(number) * time.Minute)
	return backup.RunRecord{
		Subject: subject, PassAt: at, Source: backup.SourceDatabaseKey, StartedAt: at, FinishedAt: at.Add(time.Second),
		Result: result, DestinationID: "bkd_1", DestinationName: "store",
	}
}

// Un plan garde ses MaxRunsPerPlan dernières lignes ; les plus anciennes
// partent à l'écriture de la suivante, et un autre plan n'y perd rien.
func TestBackupRuns_PurgeKeepsTheNewestOfEachPlan(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.RecordBackupRun(ctx, runRecord("other", 0, backup.ResultOK)); err != nil {
		t.Fatal(err)
	}
	for number := 1; number <= backup.MaxRunsPerPlan+25; number++ {
		if err := db.RecordBackupRun(ctx, runRecord(backup.SubjectDatabase, number, backup.ResultOK)); err != nil {
			t.Fatal(err)
		}
	}
	if kept := count(t, db, `SELECT COUNT(*) FROM backup_runs WHERE subject = 'database'`); kept != backup.MaxRunsPerPlan {
		t.Fatalf("%d lines kept, want %d", kept, backup.MaxRunsPerPlan)
	}
	if kept := count(t, db, `SELECT COUNT(*) FROM backup_runs WHERE subject = 'other'`); kept != 1 {
		t.Fatalf("the other plan kept %d lines", kept)
	}
	runs, err := db.ListBackupRuns(ctx, backup.RunQuery{Subject: backup.SubjectDatabase, Limit: backup.MaxRunsPerPlan})
	if err != nil || len(runs) != backup.MaxRunsPerPlan {
		t.Fatalf("runs %d %v", len(runs), err)
	}
	newest, oldest := runs[0], runs[len(runs)-1]
	if !newest.StartedAt.Equal(backupsNow.Add(time.Duration(backup.MaxRunsPerPlan+25)*time.Minute)) ||
		!oldest.StartedAt.Equal(backupsNow.Add(26*time.Minute)) {
		t.Fatalf("newest %v, oldest %v", newest.StartedAt, oldest.StartedAt)
	}
}

// La taille inconnue reste inconnue, pas zéro ; les comptes du dernier passage
// ne prennent que les lignes de la tentative que le plan garde.
func TestBackupRuns_UnknownSizeAndLastPassCounts(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seedBackupPlan(t, db)
	if err := db.NoteBackupAttempt(ctx, backup.SubjectDatabase, backupsNow.Add(2*time.Minute), "", ""); err != nil {
		t.Fatal(err)
	}
	size := int64(1234)
	old := runRecord(backup.SubjectDatabase, 1, backup.ResultFailed)
	ok := runRecord(backup.SubjectDatabase, 2, backup.ResultOK)
	ok.SizeBytes = &size
	failed := runRecord(backup.SubjectDatabase, 2, backup.ResultFailed)
	for _, record := range []backup.RunRecord{old, ok, failed} {
		if err := db.RecordBackupRun(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	runs, err := db.ListBackupRuns(ctx, backup.RunQuery{Subject: backup.SubjectDatabase})
	if err != nil || len(runs) != 3 || runs[0].SizeBytes != nil || runs[1].SizeBytes == nil || *runs[1].SizeBytes != size {
		t.Fatalf("runs %+v %v", runs, err)
	}
	counts, err := db.LastPassCounts(ctx)
	if err != nil || counts[backup.SubjectDatabase] != (backup.PassCounts{OK: 1, Failed: 1}) {
		t.Fatalf("counts %+v %v", counts, err)
	}
}
