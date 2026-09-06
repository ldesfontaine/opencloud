package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/migrations"
)

func TestMachines_FreshDatabase_HoldsTheOpenCloudMachine(t *testing.T) {
	database := openTestStore(t, openTestRoot(t), migrations.Files)

	machines, err := database.Machines(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(machines) != 1 {
		t.Fatalf("machines = %d, attendu 1", len(machines))
	}
	local := machines[0]
	if local.ID != LocalMachineID || local.Address != "127.0.0.1" || local.Port != 22 || local.Account != "opencloud" {
		t.Fatalf("machine locale = %+v", local)
	}
	if local.CreatedAt.IsZero() {
		t.Fatal("la date de création doit être lue")
	}
}

func TestMachine_UnknownID_IsNotFound(t *testing.T) {
	database := openTestStore(t, openTestRoot(t), migrations.Files)

	_, err := database.Machine(context.Background(), "absente")

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, attendu ErrNotFound", err)
	}
}

func TestInsertMachine_DeclaresAMachineNoProbeHasSeenYet(t *testing.T) {
	database := openTestStore(t, openTestRoot(t), migrations.Files)
	created := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)

	declared := Machine{
		ID: "temoin", Name: "témoin", Address: "192.168.1.10",
		Port: 2222, Account: "opencloud", CreatedAt: created,
	}
	if err := database.InsertMachine(context.Background(), declared); err != nil {
		t.Fatal(err)
	}

	found, err := database.Machine(context.Background(), "temoin")
	if err != nil {
		t.Fatal(err)
	}
	if found.Name != "témoin" || found.Address != "192.168.1.10" || found.Port != 2222 {
		t.Fatalf("machine = %+v", found)
	}
	if !found.CreatedAt.Equal(created) {
		t.Fatalf("CreatedAt = %s, attendu %s", found.CreatedAt, created)
	}
	if !found.ProbedAt.IsZero() || found.ProbeState != "" || found.ProbeNote != "" {
		t.Fatalf("une machine déclarée n'a pas encore de remontée : %+v", found)
	}
}

func TestMachineExists_AnswersWithoutReadingTheWholeRow(t *testing.T) {
	database := openTestStore(t, openTestRoot(t), migrations.Files)

	found, err := database.MachineExists(context.Background(), LocalMachineID)
	if err != nil || !found {
		t.Fatalf("MachineExists(local) = %v, %v", found, err)
	}

	found, err = database.MachineExists(context.Background(), "absente")
	if err != nil || found {
		t.Fatalf("MachineExists(absente) = %v, %v", found, err)
	}
}

func TestUpdateMachineAccess_ChangesTheAddressAndThePort(t *testing.T) {
	database := openTestStore(t, openTestRoot(t), migrations.Files)

	if err := database.UpdateMachineAccess(context.Background(), LocalMachineID, "10.0.0.2", 2222); err != nil {
		t.Fatal(err)
	}

	found, err := database.Machine(context.Background(), LocalMachineID)
	if err != nil {
		t.Fatal(err)
	}
	if found.Address != "10.0.0.2" || found.Port != 2222 {
		t.Fatalf("machine = %+v", found)
	}
}

func TestUpdateMachineAccess_UnknownID_IsNotFound(t *testing.T) {
	database := openTestStore(t, openTestRoot(t), migrations.Files)

	err := database.UpdateMachineAccess(context.Background(), "absente", "10.0.0.2", 22)

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, attendu ErrNotFound", err)
	}
}

func TestRecordProbe_KeepsTheLastReportAndItsDate(t *testing.T) {
	database := openTestStore(t, openTestRoot(t), migrations.Files)
	at := time.Date(2026, 9, 6, 10, 30, 0, 0, time.UTC)

	if err := database.RecordProbe(context.Background(), LocalMachineID, at, ProbeSSHFailed, "connexion refusée"); err != nil {
		t.Fatal(err)
	}

	found, err := database.Machine(context.Background(), LocalMachineID)
	if err != nil {
		t.Fatal(err)
	}
	if found.ProbeState != ProbeSSHFailed || found.ProbeNote != "connexion refusée" {
		t.Fatalf("machine = %+v", found)
	}
	if !found.ProbedAt.Equal(at) {
		t.Fatalf("ProbedAt = %s, attendu %s", found.ProbedAt, at)
	}
}

func TestRecordProbe_RefusesAStateTheStatusDoesNotName(t *testing.T) {
	database := openTestStore(t, openTestRoot(t), migrations.Files)

	err := database.RecordProbe(context.Background(), LocalMachineID, time.Now(), "en-ligne", "")

	if err == nil {
		t.Fatal("un état hors des quatre du statut doit être refusé par le schéma")
	}
}

func TestRecordProbe_UnknownID_IsNotFound(t *testing.T) {
	database := openTestStore(t, openTestRoot(t), migrations.Files)

	err := database.RecordProbe(context.Background(), "absente", time.Now(), ProbeReachable, "")

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, attendu ErrNotFound", err)
	}
}
