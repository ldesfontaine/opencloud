package store

import (
	"context"
	"errors"
	"testing"

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
