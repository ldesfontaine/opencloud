package store

import (
	"context"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
)

var probeTestNow = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

// insertProbeOnLocalMachine pose la machine openCloud puis une sonde neuve.
func insertProbeOnLocalMachine(t *testing.T, db *DB) probe.Probe {
	t.Helper()
	ctx := context.Background()
	exec(t, db, `INSERT INTO machines (id, name, hostname, created_at) VALUES (?, 'opencloud', 'host', ?)`, machine.LocalID, probeTestNow.Unix())
	item := probe.Probe{
		ID: "p1", Name: "site", Kind: probe.KindHTTP, Target: "https://cloud.exemple.fr/", MachineID: machine.LocalID,
		Status: probe.StatusNew, Interval: time.Minute, Timeout: 10 * time.Second,
		FailureThreshold: 3, RecoveryThreshold: 2, Method: "GET", ExpectedStatus: "2xx", CreatedAt: probeTestNow,
	}
	if err := db.InsertProbe(ctx, item); err != nil {
		t.Fatal(err)
	}
	return item
}

// Un signal lu avant l'édition arrive après elle : son essai s'écrit, mais
// l'état qu'il avait calculé ne remplace pas celui qu'on vient de remettre
// à zéro.
func TestApplyProbeResults_DoesNotOverwriteARevisedProbe(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	insertProbeOnLocalMachine(t, db)
	stale, err := db.GetProbe(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}

	revised := stale
	revised.Target = "https://autre.exemple.fr/"
	revised.RevisedAt = probeTestNow.Add(time.Minute)
	if err := db.UpdateProbe(ctx, probe.Revision{Probe: revised, ResetsState: true, ClearsCertificate: true}); err != nil {
		t.Fatal(err)
	}

	advanced := stale
	advanced.Status = probe.StatusUp
	advanced.ConsecutiveSuccesses = 1
	written, err := db.ApplyProbeResults(ctx, probe.Changes{
		Now:     probeTestNow,
		Results: []probe.Result{{ProbeID: "p1", CheckedAt: probeTestNow, Outcome: probe.OutcomeUp, DurationMs: 5}},
		Probes:  []probe.Probe{advanced},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 0 {
		t.Fatalf("une sonde dont l'état n'a pas été écrit ne doit pas être rendue: %+v", written)
	}

	after, err := db.GetProbe(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != probe.StatusNew || after.ConsecutiveSuccesses != 0 || after.Target != "https://autre.exemple.fr/" {
		t.Fatalf("l'état remis à zéro a été écrasé: %+v", after)
	}
	results, err := db.ListProbeResults(ctx, "p1", 10)
	if err != nil || len(results) != 1 {
		t.Fatalf("l'essai doit être dans l'histoire: %+v (%v)", results, err)
	}
}

// Sans révision, rien ne s'oppose à l'état : l'écriture passe comme avant.
func TestApplyProbeResults_WritesTheStateOfAProbeNeverRevised(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	insertProbeOnLocalMachine(t, db)
	known, err := db.GetProbe(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}

	known.Status = probe.StatusUp
	known.ConsecutiveSuccesses = 1
	written, err := db.ApplyProbeResults(ctx, probe.Changes{Now: probeTestNow, Probes: []probe.Probe{known}})
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 || written[0].ID != "p1" {
		t.Fatalf("la sonde écrite doit être rendue: %+v", written)
	}
	after, err := db.GetProbe(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != probe.StatusUp || after.ConsecutiveSuccesses != 1 {
		t.Fatalf("l'état n'a pas été écrit: %+v", after)
	}
}

// Une pause posée entre la lecture de la sonde et son édition tient : la
// base décide, pas la sonde lue plus tôt.
func TestUpdateProbe_ResetKeepsAPausedProbePaused(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	insertProbeOnLocalMachine(t, db)
	read, err := db.GetProbe(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetProbeStatus(ctx, "p1", probe.StatusPaused); err != nil {
		t.Fatal(err)
	}

	read.RevisedAt = probeTestNow
	if err := db.UpdateProbe(ctx, probe.Revision{Probe: read, ResetsState: true}); err != nil {
		t.Fatal(err)
	}
	after, err := db.GetProbe(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != probe.StatusPaused || !after.RevisedAt.Equal(probeTestNow) {
		t.Fatalf("la pause devait tenir et la révision être posée: %+v", after)
	}
}

func TestUpdateProbe_UnknownProbeIsNotFound(t *testing.T) {
	db := openTestDB(t)
	err := db.UpdateProbe(context.Background(), probe.Revision{Probe: probe.Probe{ID: "nope", MachineID: machine.LocalID}})
	if err != probe.ErrNotFound {
		t.Fatalf("attendu %v, obtenu %v", probe.ErrNotFound, err)
	}
}

// revised_at garde la milliseconde : c'est elle qui départage un essai de
// l'ancien réglage pris dans la seconde de l'édition.
func TestUpdateProbe_RevisedAtKeepsTheMillisecond(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	insertProbeOnLocalMachine(t, db)
	read, err := db.GetProbe(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	read.RevisedAt = probeTestNow.Add(250 * time.Millisecond)
	if err := db.UpdateProbe(ctx, probe.Revision{Probe: read, ResetsState: true}); err != nil {
		t.Fatal(err)
	}
	after, err := db.GetProbe(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if !after.RevisedAt.Equal(read.RevisedAt) {
		t.Fatalf("révision attendue à %s, relue à %s", read.RevisedAt, after.RevisedAt)
	}
}

// writeObservedState écrit l'état d'une sonde comme un signal le rapporte,
// avec le certificat qu'elle a vu (nil : aucun).
func writeObservedState(t *testing.T, db *DB, item probe.Probe, certificate *probe.Certificate) {
	t.Helper()
	item.Status = probe.StatusUp
	item.LastCheckedAt = probeTestNow
	item.Certificate = certificate
	written, err := db.ApplyProbeResults(context.Background(), probe.Changes{Now: probeTestNow, Probes: []probe.Probe{item}})
	if err != nil || len(written) != 1 {
		t.Fatalf("écrire l'état : %v %v", written, err)
	}
}

// Le certificat vit dans probe_certificates : écrit avec l'état, relu avec
// la sonde, effacé par un état écrit sans certificat (le composant, lui,
// garde le précédent quand un essai n'en voit pas), par une édition qui
// change la cible, et par la suppression de la sonde.
func TestProbeCertificate_FollowsTheChecksTheTargetAndTheProbe(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	item := insertProbeOnLocalMachine(t, db)
	seen := &probe.Certificate{
		Subject: "CN=cloud.exemple.fr", Issuer: "CN=R11", Fingerprint: "ab12",
		NotBefore: probeTestNow.Add(-24 * time.Hour), NotAfter: probeTestNow.Add(10 * 24 * time.Hour),
		ChainValid: true, HostnameMatch: true, OCSP: probe.OCSPGood,
	}

	if got, _ := db.GetProbe(ctx, "p1"); got.Certificate != nil {
		t.Fatalf("une sonde neuve n'a pas de certificat : %+v", got.Certificate)
	}
	writeObservedState(t, db, item, seen)
	got, err := db.GetProbe(ctx, "p1")
	if err != nil || got.Certificate == nil {
		t.Fatalf("certificat relu = %+v (%v)", got.Certificate, err)
	}
	// Les dates se relisent à la seconde, dans le fuseau local.
	read := *got.Certificate
	read.NotBefore, read.NotAfter = read.NotBefore.UTC(), read.NotAfter.UTC()
	if read != *seen {
		t.Fatalf("certificat relu = %+v, attendu %+v", read, *seen)
	}
	counted, err := db.CountProbeCertificates(ctx, probeTestNow, probeTestNow.Add(30*24*time.Hour))
	if err != nil || counted.Total != 1 || counted.Expiring != 1 || !counted.Soonest.Equal(seen.NotAfter) {
		t.Fatalf("comptes = %+v (%v)", counted, err)
	}

	writeObservedState(t, db, item, nil)
	if got := count(t, db, `SELECT COUNT(*) FROM probe_certificates`); got != 0 {
		t.Fatalf("un état écrit sans certificat doit effacer le précédent : %d ligne(s)", got)
	}

	writeObservedState(t, db, item, seen)
	if err := db.UpdateProbe(ctx, probe.Revision{Probe: item}); err != nil {
		t.Fatal(err)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM probe_certificates`); got != 1 {
		t.Fatal("une édition qui garde la cible garde le certificat")
	}
	if err := db.UpdateProbe(ctx, probe.Revision{Probe: item, ClearsCertificate: true}); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.GetProbe(ctx, "p1"); got.Certificate != nil {
		t.Fatalf("une édition qui change la cible efface le certificat : %+v", got.Certificate)
	}

	writeObservedState(t, db, item, seen)
	if err := db.DeleteProbe(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM probe_certificates`); got != 0 {
		t.Fatalf("le certificat part avec sa sonde : %d ligne(s)", got)
	}
}

// Une sonde révisée depuis la lecture de l'essai garde son certificat : la
// garde de revised_at vaut pour lui comme pour l'état.
func TestProbeCertificate_IsNotWrittenForARevisedProbe(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	stale := insertProbeOnLocalMachine(t, db)
	revised := stale
	revised.RevisedAt = probeTestNow.Add(time.Minute)
	if err := db.UpdateProbe(ctx, probe.Revision{Probe: revised, ResetsState: true, ClearsCertificate: true}); err != nil {
		t.Fatal(err)
	}
	stale.Certificate = &probe.Certificate{Fingerprint: "ab12", NotAfter: probeTestNow}
	if _, err := db.ApplyProbeResults(ctx, probe.Changes{Now: probeTestNow, Probes: []probe.Probe{stale}}); err != nil {
		t.Fatal(err)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM probe_certificates`); got != 0 {
		t.Fatalf("le certificat d'un essai d'avant l'édition s'est écrit : %d ligne(s)", got)
	}
}
