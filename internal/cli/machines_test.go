package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/runner"
	"github.com/ldesfontaine/opencloud/internal/store"
)

func newTestAccess(t *testing.T) machineAccess {
	t.Helper()

	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return machineAccess{root: root}
}

func testMachine() store.Machine {
	return store.Machine{
		ID: "temoin", Name: "témoin", Address: "192.168.1.10",
		Port: 2222, Account: "opencloud",
	}
}

func markEnrolled(t *testing.T, access machineAccess, id string) {
	t.Helper()

	directory := filepath.Join(access.root.Name(), "machines", id)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "enrolled"), []byte("2026-09-06T10:00:00Z\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMachineAccess_Prepare_RendLaCommandeACollerSurLaMachine(t *testing.T) {
	access := newTestAccess(t)

	command, err := access.Prepare(context.Background(), testMachine())
	if err != nil {
		t.Fatalf("Prepare : %v", err)
	}

	if !strings.HasPrefix(command, "echo '") || !strings.HasSuffix(command, "| base64 -d | sudo bash") {
		t.Errorf("commande : %q", command)
	}
	if _, err := os.Stat(filepath.Join(access.root.Name(), "machines/temoin/id_ed25519")); err != nil {
		t.Errorf("la paire de la machine n'a pas été posée : %v", err)
	}
}

func TestMachineAccess_For_SertTouteMachineEnroleeEtRefuseLesAutres(t *testing.T) {
	access := newTestAccess(t)

	if _, err := access.For(context.Background(), testMachine()); !errors.Is(err, runner.ErrNotEnrolled) {
		t.Fatalf("une machine non enrôlée doit être refusée, reçu %v", err)
	}

	if _, err := access.Prepare(context.Background(), testMachine()); err != nil {
		t.Fatal(err)
	}
	markEnrolled(t, access, "temoin")

	client, err := access.For(context.Background(), testMachine())
	if err != nil {
		t.Fatalf("For : %v", err)
	}
	if client == nil {
		t.Fatal("transport nul pour une machine enrôlée")
	}
}

func TestMachineProbes_For_NeSondeQueLesMachinesEnrolees(t *testing.T) {
	access := newTestAccess(t)
	probes := machineProbes{access: access}

	if _, err := probes.For(context.Background(), testMachine()); err == nil {
		t.Fatal("une machine non enrôlée n'a rien à sonder")
	}

	if _, err := access.Prepare(context.Background(), testMachine()); err != nil {
		t.Fatal(err)
	}
	markEnrolled(t, access, "temoin")

	prober, err := probes.For(context.Background(), testMachine())
	if err != nil {
		t.Fatalf("For : %v", err)
	}
	if prober == nil {
		t.Fatal("sonde nulle pour une machine enrôlée")
	}
}

func TestMachineAccess_Status_DitDepuisQuandLaMachineEstEnrolee(t *testing.T) {
	access := newTestAccess(t)

	status, err := access.Status(context.Background(), "temoin")
	if err != nil {
		t.Fatalf("Status : %v", err)
	}
	if status.Enrolled {
		t.Fatal("machine annoncée enrôlée sans enrôlement")
	}

	if _, err := access.Prepare(context.Background(), testMachine()); err != nil {
		t.Fatal(err)
	}
	markEnrolled(t, access, "temoin")

	status, err = access.Status(context.Background(), "temoin")
	if err != nil {
		t.Fatalf("Status : %v", err)
	}
	if !status.Enrolled || status.Since.IsZero() {
		t.Fatalf("statut = %+v", status)
	}
}
