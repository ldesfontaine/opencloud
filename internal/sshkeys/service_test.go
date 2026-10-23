package sshkeys_test

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/sshkeys"
	"github.com/ldesfontaine/opencloud/internal/store"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
)

const (
	machineOne   = "11111111-1111-4111-8111-111111111111"
	machineTwo   = "22222222-2222-4222-8222-222222222222"
	machineThree = "33333333-3333-4333-8333-333333333333"
	fingerprintA = "SHA256:nThbg6kXUpJWGl7E1IGOCspRomTxdCARLviKw6E5SY8"
	fingerprintB = "SHA256:Qa1b2c3d4e5f6g7h8i9j0KLMNOPQRSTUVWXYZabcdef"
)

type bench struct {
	db       *store.DB
	machines *machine.Service
	service  *sshkeys.Service
	now      time.Time
}

func newBench(t *testing.T) *bench {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	db := storetest.Open(t)
	b := &bench{db: db, machines: machine.New(db, machine.NewSessions(), logger), service: sshkeys.New(db, logger), now: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)}
	b.service.SetClock(func() time.Time { return b.now })
	return b
}

func key(file, fingerprint, comment string, options ...string) sshkeys.Key {
	if options == nil {
		options = []string{}
	}
	return sshkeys.Key{File: file, Fingerprint: fingerprint, Type: "ssh-ed25519", Bits: 256, Comment: comment, Options: options}
}

func TestRecord_KeepsTheReportAndReadsItBack(t *testing.T) {
	ctx := context.Background()
	b := newBench(t)
	storetest.EnrollMachine(t, b.machines, machineOne, "vps-paris-1")
	report := sshkeys.Report{Accounts: []sshkeys.Account{
		{Name: "root", UID: 0, State: sshkeys.StateRead, Ignored: 2, Keys: []sshkeys.Key{key("authorized_keys", fingerprintA, "lucas@pc", "command", "from")}},
		{Name: "lucas", UID: 1000, State: sshkeys.StateDenied, Keys: []sshkeys.Key{}},
		{Name: "deploy", UID: 1001, State: sshkeys.StateRead, Truncated: true, Keys: []sshkeys.Key{
			key("authorized_keys", fingerprintB, "ci"), key("authorized_keys2", fingerprintA, "")}},
	}, Truncated: true}

	if err := b.service.Record(ctx, machineOne, report); err != nil {
		t.Fatal(err)
	}

	got, err := b.service.Machine(ctx, machineOne)
	if err != nil {
		t.Fatal(err)
	}
	if got.MachineID != machineOne || got.MachineName != "vps-paris-1" || !got.ReportedAt.Equal(b.now) || !got.Truncated {
		t.Fatalf("relevé : %+v", got)
	}
	// Les comptes viennent par uid, leurs clés par fichier puis commentaire.
	want := []sshkeys.Account{report.Accounts[0], report.Accounts[1], report.Accounts[2]}
	if !reflect.DeepEqual(got.Accounts, want) {
		t.Fatalf("comptes :\n got %+v\nwant %+v", got.Accounts, want)
	}
}

func TestRecord_ReplacesWhatTheMachineHad(t *testing.T) {
	ctx := context.Background()
	b := newBench(t)
	storetest.EnrollMachine(t, b.machines, machineOne, "vps-paris-1")
	first := sshkeys.Report{Accounts: []sshkeys.Account{
		{Name: "root", State: sshkeys.StateRead, Keys: []sshkeys.Key{key("authorized_keys", fingerprintA, "ancienne")}},
		{Name: "lucas", UID: 1000, State: sshkeys.StateRead, Keys: []sshkeys.Key{key("authorized_keys", fingerprintB, "")}},
	}}
	second := sshkeys.Report{Accounts: []sshkeys.Account{
		{Name: "root", State: sshkeys.StateRead, Keys: []sshkeys.Key{key("authorized_keys", fingerprintB, "neuve")}},
	}}
	if err := b.service.Record(ctx, machineOne, first); err != nil {
		t.Fatal(err)
	}
	b.now = b.now.Add(10 * time.Minute)

	if err := b.service.Record(ctx, machineOne, second); err != nil {
		t.Fatal(err)
	}

	got, err := b.service.Machine(ctx, machineOne)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Accounts, second.Accounts) || !got.ReportedAt.Equal(b.now) || got.Truncated {
		t.Fatalf("relevé : %+v", got)
	}
}

func TestRecord_AnInvalidReportIsRefusedWholeAndKeepsTheOldOne(t *testing.T) {
	ctx := context.Background()
	b := newBench(t)
	storetest.EnrollMachine(t, b.machines, machineOne, "vps-paris-1")
	good := sshkeys.Report{Accounts: []sshkeys.Account{{Name: "root", State: sshkeys.StateRead, Keys: []sshkeys.Key{key("authorized_keys", fingerprintA, "bonne")}}}}
	if err := b.service.Record(ctx, machineOne, good); err != nil {
		t.Fatal(err)
	}
	bad := sshkeys.Report{Accounts: []sshkeys.Account{
		{Name: "root", State: sshkeys.StateRead, Keys: []sshkeys.Key{key("authorized_keys", fingerprintB, "ok")}},
		{Name: "lucas", State: "hacked"},
	}}

	err := b.service.Record(ctx, machineOne, bad)

	if !errors.Is(err, sshkeys.ErrReportInvalid) {
		t.Fatalf("erreur : %v", err)
	}
	got, _ := b.service.Machine(ctx, machineOne)
	if !reflect.DeepEqual(got.Accounts, good.Accounts) {
		t.Fatalf("le relevé a bougé : %+v", got.Accounts)
	}
}

func TestMachine_BeforeAnyReport_IsEmptyAndUndated(t *testing.T) {
	ctx := context.Background()
	b := newBench(t)
	storetest.EnrollMachine(t, b.machines, machineOne, "vps-paris-1")

	got, err := b.service.Machine(ctx, machineOne)
	if err != nil {
		t.Fatal(err)
	}
	if !got.ReportedAt.IsZero() || got.Accounts == nil || len(got.Accounts) != 0 {
		t.Fatalf("relevé : %+v", got)
	}
}

func TestMachine_Unknown_IsNotFound(t *testing.T) {
	b := newBench(t)
	if _, err := b.service.Machine(context.Background(), "inconnue"); !errors.Is(err, machine.ErrNotFound) {
		t.Fatalf("erreur : %v", err)
	}
}

func TestMatrix_ListsEveryMachineByNameWithItsOwnKeysOnly(t *testing.T) {
	ctx := context.Background()
	b := newBench(t)
	storetest.EnrollMachine(t, b.machines, machineTwo, "bastion")
	storetest.EnrollMachine(t, b.machines, machineOne, "vps-paris-1")
	storetest.EnrollMachine(t, b.machines, machineThree, "ancien-agent")
	for id, fingerprint := range map[string]string{machineOne: fingerprintA, machineTwo: fingerprintB} {
		report := sshkeys.Report{Accounts: []sshkeys.Account{{Name: "root", State: sshkeys.StateRead, Keys: []sshkeys.Key{key("authorized_keys", fingerprint, "m1")}}}}
		if err := b.service.Record(ctx, id, report); err != nil {
			t.Fatal(err)
		}
	}

	got, err := b.service.Matrix(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 3 || got[0].MachineName != "ancien-agent" || got[1].MachineName != "bastion" || got[2].MachineName != "vps-paris-1" {
		t.Fatalf("machines : %+v", got)
	}
	if !got[0].ReportedAt.IsZero() || len(got[0].Accounts) != 0 {
		t.Fatalf("la machine sans relevé : %+v", got[0])
	}
	if got[1].Accounts[0].Keys[0].Fingerprint != fingerprintB || got[2].Accounts[0].Keys[0].Fingerprint != fingerprintA || got[2].Accounts[0].Keys[0].Comment != "m1" {
		t.Fatalf("les clés se sont mélangées : %+v", got)
	}
}

func TestRecord_AnUnknownMachine_IsAnErrorNotARefusal(t *testing.T) {
	b := newBench(t)
	report := sshkeys.Report{Accounts: []sshkeys.Account{{Name: "root", State: sshkeys.StateNone, Keys: []sshkeys.Key{}}}}

	err := b.service.Record(context.Background(), "fantome", report)

	if err == nil || errors.Is(err, sshkeys.ErrReportInvalid) {
		t.Fatalf("erreur : %v", err)
	}
}

type recordingWatcher struct{ machines []string }

func (w *recordingWatcher) SSHKeysChanged(machineID string) {
	w.machines = append(w.machines, machineID)
}

func TestRecord_TellsTheWatcherOnlyWhenTheReportIsKept(t *testing.T) {
	ctx := context.Background()
	b := newBench(t)
	watcher := &recordingWatcher{}
	b.service.SetWatcher(watcher)
	storetest.EnrollMachine(t, b.machines, machineOne, "vps-paris-1")

	_ = b.service.Record(ctx, machineOne, sshkeys.Report{Accounts: []sshkeys.Account{{Name: "root", State: "hacked"}}})
	_ = b.service.Record(ctx, "fantome", sshkeys.Report{})
	if len(watcher.machines) != 0 {
		t.Fatalf("le direct a été prévenu d'un relevé non gardé : %v", watcher.machines)
	}

	if err := b.service.Record(ctx, machineOne, sshkeys.Report{}); err != nil {
		t.Fatal(err)
	}
	if len(watcher.machines) != 1 || watcher.machines[0] != machineOne {
		t.Fatalf("direct : %v", watcher.machines)
	}
}
