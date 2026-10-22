package backup_test

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/backup"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/store"
)

// Sans clé, rien ne part et la page sait pourquoi : la clé du refus est
// écrite dans le plan, qui garde sa tentative.
func TestRun_WithoutKeyring_NotesTheRefusal(t *testing.T) {
	b := newBench(t, rights)
	destination, err := b.backups.CreateDestination(operator(), backup.DestinationDefinition{Name: "store-1", MachineID: machineID, Path: "/srv/backups"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.backups.RunNow(operator()); !errors.Is(err, backup.ErrPlanMissing) {
		t.Fatalf("no plan: %v", err)
	}
	if _, err := b.backups.SavePlan(operator(), backup.PlanDefinition{DestinationID: destination.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.backups.RunNow(operator()); !errors.Is(err, backup.ErrKeyringMissing) {
		t.Fatalf("no key: %v", err)
	}
	plan, err := b.db.GetBackupPlan(context.Background())
	if err != nil || plan.LastError != "backup.keyring_missing" || plan.LastRunID != "" || !plan.LastAttemptAt.Equal(benchNow) || plan.Keep != backup.DefaultKeep {
		t.Fatalf("plan %+v %v", plan, err)
	}
	if len(b.commands.orders) != 0 {
		t.Fatalf("orders were sent: %+v", b.commands.kinds)
	}
	if entries, _ := os.ReadDir(b.stateDir.Name() + "/outgoing"); len(entries) != 0 {
		t.Fatalf("a parcel was left: %v", entries)
	}
}

// Une machine hors ligne ou dont l'agent a dit ne pas avoir le droit : rien
// ne part, le colis n'est même pas préparé.
func TestRun_RefusesAnOfflineMachineAndALostRight(t *testing.T) {
	b := newBench(t, rights)
	b.ready()
	b.machines.Disconnect(b.session)
	if _, err := b.backups.RunNow(operator()); !errors.Is(err, backup.ErrMachineOffline) {
		t.Fatalf("offline: %v", err)
	}
	if plan, _ := b.db.GetBackupPlan(context.Background()); plan.LastError != "backup.machine_offline" {
		t.Fatalf("plan %+v", plan)
	}
	// L'agent revient avec moins de droits que celui de la destination.
	enrolled, err := b.machines.Get(context.Background(), machineID)
	if err != nil {
		t.Fatal(err)
	}
	b.session, err = b.machines.Connect(context.Background(), enrolled.Machine, "192.0.2.1", "v0.0.1", "backup.receive=/srv/autre")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.backups.RunNow(operator()); !errors.Is(err, backup.ErrRightMissing) {
		t.Fatalf("lost right: %v", err)
	}
	if len(b.commands.orders) != 0 {
		t.Fatalf("orders were sent: %+v", b.commands.kinds)
	}
}

// Un ordre qui ne part pas (flux refusé) ne laisse pas de colis derrière lui.
func TestRun_DiscardsTheParcelOfAnOrderThatDidNotLeave(t *testing.T) {
	b := newBench(t, rights)
	b.ready()
	b.commands.refuse = machine.ErrBusy
	run, err := b.backups.RunNow(operator())
	if err != nil {
		t.Fatal(err)
	}
	if run.State != action.StateFailed || run.Reason != action.ReasonUndelivered {
		t.Fatalf("run %+v", run)
	}
	if entries, _ := os.ReadDir(b.stateDir.Name() + "/outgoing"); len(entries) != 0 {
		t.Fatalf("a parcel was left: %v", entries)
	}
}

// snapshotSpy relève les droits du fichier où SQLite écrit l'instantané, et
// peut faire échouer la préparation juste après lui.
type snapshotSpy struct {
	*store.DB
	mode os.FileMode
	fail error
}

func (s *snapshotSpy) SnapshotDatabase(ctx context.Context, path string) error {
	if err := s.DB.SnapshotDatabase(ctx, path); err != nil {
		return err
	}
	if info, err := os.Stat(path); err == nil {
		s.mode = info.Mode().Perm()
	}
	return s.fail
}

// L'instantané en clair est en 0600 pendant qu'il existe ; une préparation
// qui échoue après lui ne laisse rien dans outgoing/, et le plan dit
// backup.prepare_failed.
func TestRun_ClearSnapshotIsPrivateAndNeverOutlivesAFailure(t *testing.T) {
	b := newBench(t, rights)
	b.ready()
	spy := &snapshotSpy{DB: b.db}
	service := backup.New(spy, b.machines, b.stateDir, slog.New(slog.DiscardHandler))
	service.SetClock(func() time.Time { return b.now })
	service.SetSender(b.actions)
	service.SetParcels(b.outbox)
	if _, err := service.RunNow(operator()); err != nil {
		t.Fatal(err)
	}
	if spy.mode != 0o600 {
		t.Fatalf("the clear snapshot was %o", spy.mode)
	}

	spy.fail = errors.New("disque plein")
	b.commands.orders, b.commands.kinds = nil, nil
	if _, err := service.RunNow(operator()); err == nil {
		t.Fatal("a failed preparation must fail the run")
	}
	if len(b.commands.orders) != 0 {
		t.Fatalf("orders were sent: %+v", b.commands.kinds)
	}
	parcels, err := os.ReadDir(b.stateDir.Name() + "/outgoing")
	if err != nil || len(parcels) != 1 {
		t.Fatalf("only the parcel of the first run may stay: %v %v", parcels, err)
	}
	if plan, _ := b.db.GetBackupPlan(context.Background()); plan.LastError != backup.PrepareFailedKey {
		t.Fatalf("plan %+v", plan)
	}
}

// Le plan ne retient pas la destination qu'il utilise ; une fois arrêté, elle
// se retire. La machine qui la porte est retenue tant qu'elle existe.
func TestDeleteDestination_IsHeldBackWhileThePlanUsesIt(t *testing.T) {
	b := newBench(t, rights)
	_, destination := b.ready()
	err := b.backups.DeleteDestination(operator(), destination.ID)
	requireRefusal(t, err, backup.ErrDestinationInUse)
	if err := b.machines.Remove(operator(), machineID); !errors.Is(err, machine.ErrHasBackupDestinations) {
		t.Fatalf("remove the machine: %v", err)
	}
	if err := b.backups.DeletePlan(operator()); err != nil {
		t.Fatal(err)
	}
	if err := b.backups.DeleteDestination(operator(), destination.ID); err != nil {
		t.Fatal(err)
	}
	if err := b.backups.DeleteDestination(operator(), destination.ID); !errors.Is(err, backup.ErrNotFound) {
		t.Fatalf("twice: %v", err)
	}
	if err := b.backups.DeletePlan(operator()); !errors.Is(err, backup.ErrNotFound) {
		t.Fatalf("no plan left: %v", err)
	}
}

func TestSavePlan_ChecksItsDestinationAndItsKeep(t *testing.T) {
	b := newBench(t, rights)
	_, destination := b.ready()
	_, err := b.backups.SavePlan(operator(), backup.PlanDefinition{DestinationID: "bkd_inconnue", Keep: 3})
	requireRefusal(t, err, backup.ErrDestinationUnknown)
	for _, keep := range []int{-1, 366} {
		_, err := b.backups.SavePlan(operator(), backup.PlanDefinition{DestinationID: destination.ID, Keep: keep})
		requireRefusal(t, err, backup.ErrKeepInvalid)
	}
	// Changer le plan ne change pas son histoire.
	if _, err := b.backups.RunNow(operator()); err != nil {
		t.Fatal(err)
	}
	saved, err := b.backups.SavePlan(operator(), backup.PlanDefinition{DestinationID: destination.ID, Keep: 30})
	if err != nil || saved.Keep != 30 || saved.LastRunID == "" || saved.LastAttemptAt.IsZero() {
		t.Fatalf("saved %+v %v", saved, err)
	}
	if saved.Schedule != backup.ScheduleDaily || saved.Subject != backup.SubjectDatabase {
		t.Fatalf("saved %+v", saved)
	}
}

// La nuit lance une sauvegarde au premier tour après 3 h, une seule par nuit,
// sous l'acteur « backup ».
func TestRunIfDue_RunsOncePerNightUnderTheBackupRule(t *testing.T) {
	b := newBench(t, rights)
	b.ready()
	b.now = time.Date(2026, 10, 22, 2, 50, 0, 0, time.UTC)
	b.backups.RunIfDue(context.Background())
	// Le plan vient d'être créé : aucune tentative depuis le 3 h d'hier.
	if len(b.commands.orders) != 2 {
		t.Fatalf("a new plan runs at once: %+v", b.commands.kinds)
	}
	b.now = time.Date(2026, 10, 22, 3, 5, 0, 0, time.UTC)
	b.backups.RunIfDue(context.Background())
	if len(b.commands.orders) != 4 {
		t.Fatalf("after 3 a.m. the night runs: %d orders", len(b.commands.orders))
	}
	b.now = time.Date(2026, 10, 22, 3, 20, 0, 0, time.UTC)
	b.backups.RunIfDue(context.Background())
	b.now = time.Date(2026, 10, 22, 23, 0, 0, 0, time.UTC)
	b.backups.RunIfDue(context.Background())
	if len(b.commands.orders) != 4 {
		t.Fatalf("a second run the same night: %d orders", len(b.commands.orders))
	}
	events, _ := b.db.ListAuditEvents(context.Background(), audit.Query{Limit: 10, ObjectKind: audit.ObjectBackupPlan})
	if events[0].Action != audit.BackupPlanRun || events[0].Actor.Kind != audit.ActorRule || events[0].Actor.Name != "backup" {
		t.Fatalf("event %+v", events[0])
	}
}

func TestDue_OncePerDayAfterThree(t *testing.T) {
	paris := time.FixedZone("CEST", 2*3600)
	at := func(day, hour, minute int) time.Time { return time.Date(2026, 10, day, hour, minute, 0, 0, paris) }
	cases := []struct {
		name string
		now  time.Time
		last time.Time
		want bool
	}{
		{"never attempted", at(21, 10, 0), time.Time{}, true},
		{"attempted after tonight's 3 a.m.", at(21, 10, 0), at(21, 3, 15), false},
		{"attempted one minute before 3 a.m.", at(21, 3, 10), at(21, 2, 59), true},
		{"yesterday's night, today's not come", at(21, 2, 0), at(20, 3, 10), false},
		{"yesterday's night was missed", at(21, 2, 0), at(19, 3, 10), true},
		{"3 a.m. passed, last night's attempt only", at(21, 3, 0), at(20, 3, 15), true},
		{"3 a.m. sharp", at(21, 3, 0), at(21, 3, 0), false},
		{"a plan made at 4 a.m.", at(21, 4, 0), at(21, 4, 0), false},
		{"the manual run at noon counts", at(21, 13, 0), at(21, 12, 0), false},
		{"the day after the manual run", at(22, 3, 5), at(21, 12, 0), true},
	}
	for _, c := range cases {
		if got := backup.Due(c.now, backup.Plan{LastAttemptAt: c.last}); got != c.want {
			t.Errorf("%s: %t, want %t", c.name, got, c.want)
		}
	}
}

// Le jour du changement d'heure, 3 h reste 3 h sur l'horloge du serveur.
func TestDue_FollowsTheWallClockAcrossADaylightChange(t *testing.T) {
	zone, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Skip("no tzdata")
	}
	changeDay := time.Date(2026, 3, 29, 3, 10, 0, 0, zone)
	if !backup.Due(changeDay, backup.Plan{LastAttemptAt: time.Date(2026, 3, 28, 3, 10, 0, 0, zone)}) {
		t.Error("after 3 a.m. on the day the clocks change, the night is due")
	}
	if backup.Due(changeDay, backup.Plan{LastAttemptAt: time.Date(2026, 3, 29, 3, 5, 0, 0, zone)}) {
		t.Error("already attempted that night")
	}
}

// Une archive qui porte un fichier hors des trois, un chemin qui remonte ou
// un doublon est refusée, et rien n'est écrit sous son nom définitif.
func TestExtractArchive_RefusesAnUnknownEntry(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	for _, evil := range []string{"../x", "/etc/passwd", "sous/dossier", "autre.txt", brand.DatabaseFile + ".part"} {
		archive := fabricate(t, identity.Recipient(), []entry{
			{brand.DatabaseFile, "base"}, {evil, "intrus"}, {brand.SecretKeyFile, "a"}, {brand.ServerKeyFile, "b"},
		})
		target := openEmptyRoot(t)
		err := backup.ExtractArchive(bytes.NewReader(archive), identity, target)
		if !errors.Is(err, backup.ErrUnexpectedEntry) {
			t.Errorf("%q: %v", evil, err)
		}
		assertEmpty(t, target)
	}
	twice := fabricate(t, identity.Recipient(), []entry{
		{brand.DatabaseFile, "base"}, {brand.DatabaseFile, "encore"}, {brand.SecretKeyFile, "a"}, {brand.ServerKeyFile, "b"},
	})
	target := openEmptyRoot(t)
	if err := backup.ExtractArchive(bytes.NewReader(twice), identity, target); !errors.Is(err, backup.ErrUnexpectedEntry) {
		t.Errorf("a duplicate: %v", err)
	}
	assertEmpty(t, target)

	incomplete := fabricate(t, identity.Recipient(), []entry{{brand.DatabaseFile, "base"}, {brand.SecretKeyFile, "a"}})
	if err := backup.ExtractArchive(bytes.NewReader(incomplete), identity, target); !errors.Is(err, backup.ErrArchiveIncomplete) {
		t.Errorf("incomplete: %v", err)
	}
	assertEmpty(t, target)

	// Un flux age tronqué ne s'authentifie pas : rien n'apparaît.
	whole := fabricate(t, identity.Recipient(), []entry{{brand.DatabaseFile, "base"}, {brand.SecretKeyFile, "a"}, {brand.ServerKeyFile, "b"}})
	if err := backup.ExtractArchive(bytes.NewReader(whole[:len(whole)-20]), identity, target); err == nil {
		t.Error("a truncated archive was accepted")
	}
	assertEmpty(t, target)

	other, _ := age.GenerateX25519Identity()
	if err := backup.ExtractArchive(bytes.NewReader(whole), other, target); err == nil {
		t.Error("an archive opened with another key")
	}

	// Un nom connu sous un autre type (un lien vers /etc/shadow) ou une clé
	// qui dépasse sa borne : refusés avant d'écrire un octet.
	for name, header := range map[string]tar.Header{
		"a symlink":  {Typeflag: tar.TypeSymlink, Name: brand.SecretKeyFile, Linkname: "/etc/shadow"},
		"a hardlink": {Typeflag: tar.TypeLink, Name: brand.SecretKeyFile, Linkname: brand.DatabaseFile},
		"too large":  {Typeflag: tar.TypeReg, Name: brand.SecretKeyFile, Mode: 0o600, Size: 64<<10 + 1},
	} {
		var out bytes.Buffer
		encrypted, err := age.Encrypt(&out, identity.Recipient())
		if err != nil {
			t.Fatal(err)
		}
		packer := tar.NewWriter(encrypted)
		if err := packer.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		_, _ = packer.Write(bytes.Repeat([]byte("x"), int(header.Size)))
		_ = packer.Close()
		_ = encrypted.Close()
		err = backup.ExtractArchive(bytes.NewReader(out.Bytes()), identity, target)
		if !errors.Is(err, backup.ErrUnexpectedEntry) && !errors.Is(err, backup.ErrEntryTooLarge) {
			t.Errorf("%s: %v", name, err)
		}
		assertEmpty(t, target)
	}
}

// La restauration rend les trois fichiers, en 0600, ce que la sauvegarde a pris.
func TestExtractArchive_RoundTrip(t *testing.T) {
	b := newBench(t, rights)
	keyring, _ := b.ready()
	if _, err := b.backups.RunNow(operator()); err != nil {
		t.Fatal(err)
	}
	var receive agentwire.BackupReceiveParams
	if err := json.Unmarshal(b.commands.orders[0].Params, &receive); err != nil {
		t.Fatal(err)
	}
	archive := downloaded(t, b.outbox, receive, agentwire.BackupFileName(benchNow))
	sealed := downloaded(t, b.outbox, receive, agentwire.BackupKeyFileName(keyring.Fingerprint))
	identity, err := backup.OpenIdentity(sealed, testPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	target := openEmptyRoot(t)
	if err := backup.ExtractArchive(bytes.NewReader(archive), identity, target); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		brand.SecretKeyFile: "clé de chiffrement de test", brand.ServerKeyFile: "ed25519-v1:clé de test",
	} {
		got, err := target.ReadFile(name)
		if err != nil || string(got) != want {
			t.Errorf("%s: %q %v", name, got, err)
		}
	}
	for _, name := range []string{brand.DatabaseFile, brand.SecretKeyFile, brand.ServerKeyFile} {
		info, err := target.Stat(name)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v %v", name, info, err)
		}
	}
}

type entry struct{ name, content string }

// fabricate emballe n'importe quels fichiers comme le ferait la sauvegarde,
// pour éprouver l'extraction avec ce qu'elle ne doit pas accepter.
func fabricate(t *testing.T, recipient age.Recipient, entries []entry) []byte {
	t.Helper()
	var out bytes.Buffer
	encrypted, err := age.Encrypt(&out, recipient)
	if err != nil {
		t.Fatal(err)
	}
	packer := tar.NewWriter(encrypted)
	for _, e := range entries {
		if err := packer.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: e.name, Mode: 0o600, Size: int64(len(e.content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := packer.Write([]byte(e.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := packer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := encrypted.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func openEmptyRoot(t *testing.T) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root
}

func assertEmpty(t *testing.T, root *os.Root) {
	t.Helper()
	entries, err := os.ReadDir(root.Name())
	if err != nil || len(entries) != 0 {
		names := []string{}
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("the target is not empty: %v %v", names, err)
	}
}
