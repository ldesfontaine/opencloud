package storetest

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/seal"
	"github.com/ldesfontaine/opencloud/internal/store"
)

// Open rend une base neuve dans un répertoire temporaire, avec une clé de
// chiffrement tirée pour l'occasion. Elle se ferme à la fin du test.
func Open(t testing.TB) *store.DB {
	t.Helper()
	_, db := OpenWithStateDir(t)
	return db
}

// OpenWithStateDir fait comme Open et rend aussi le répertoire d'état, pour
// les tests qui y lisent ou y écrivent des fichiers à côté de la base.
func OpenWithStateDir(t testing.TB) (*os.Root, *store.DB) {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	db, err := store.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	db.SetSealer(seal.Generate())
	// Enregistrée après celle du répertoire : la base se ferme la première.
	t.Cleanup(func() { _ = db.Close() })
	return root, db
}

// SaveLocalMachine inscrit la machine openCloud et son agent par la base
// seule, sans le service des machines : un jeton écrit puis consommé sous
// LocalID, comme le fait l'enrôlement de l'agent qui tourne sur la machine
// du serveur.
func SaveLocalMachine(t testing.TB, db *store.DB, at time.Time) {
	t.Helper()
	ctx := context.Background()
	cleartext, token, err := machine.NewToken(brand.LocalMachineName, "")
	if err != nil {
		t.Fatal(err)
	}
	token.CreatedAt, token.ExpiresAt = at, at.Add(machine.TokenLifetime)
	if err := db.InsertToken(ctx, token); err != nil {
		t.Fatal(err)
	}
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	local := machine.Machine{ID: machine.LocalID, Hostname: localHostname, CreatedAt: at}
	agent := machine.Agent{ID: localAgentID, PublicKey: public, Version: "v0.0.1", EnrolledAt: at}
	if _, err := db.Enroll(ctx, machine.HashToken(cleartext), local, agent, at); err != nil {
		t.Fatal(err)
	}
}

// Ce que les rendus figés voient de la machine openCloud.
const (
	localHostname = brand.ID + "-host"
	localAgentID  = "agt_0000000000000001"
	// LocalFingerprint est l'empreinte que les tests donnent à la machine du
	// serveur : l'agent qui la présente s'enrôle sous LocalID.
	LocalFingerprint = "test-server-host"
)

// InsertProbe écrit une sonde directement en base, sans les règles du
// composant : celle qu'une base ancienne garde d'avant un refus nouveau. La
// définition est complétée, la sonde naît « Nouvelle ».
func InsertProbe(t testing.TB, db *store.DB, id string, definition probe.Definition, at time.Time) probe.Probe {
	t.Helper()
	definition = definition.Complete()
	item := probe.Probe{
		ID: id, Name: definition.Name, Kind: definition.Kind, Target: definition.Target,
		MachineID: definition.MachineID, ServiceID: definition.ServiceID, Status: probe.StatusNew,
		Interval: definition.Interval, Timeout: definition.Timeout,
		FailureThreshold: definition.FailureThreshold, RecoveryThreshold: definition.RecoveryThreshold,
		Method: definition.Method, ExpectedStatus: definition.ExpectedStatus, ExpectedBody: definition.ExpectedBody,
		FollowRedirects: definition.FollowRedirects, TLS: definition.TLS, CreatedAt: at,
	}
	if err := db.InsertProbe(context.Background(), item); err != nil {
		t.Fatal(err)
	}
	return item
}

// EnsureLocalMachine enrôle l'agent de la machine openCloud par le service
// des machines, comme en vrai : un jeton, puis un agent qui présente
// l'empreinte de la machine du serveur et prend LocalID.
func EnsureLocalMachine(t testing.TB, machines *machine.Service) machine.Machine {
	t.Helper()
	ctx := context.Background()
	machines.SetServerHost(LocalFingerprint, localHostname)
	cleartext, _, err := machines.CreateToken(ctx, brand.LocalMachineName, nil)
	if err != nil {
		t.Fatal(err)
	}
	public, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id, err := machine.NewID()
	if err != nil {
		t.Fatal(err)
	}
	enrolled, err := machines.Enroll(ctx, machine.Enrollment{
		MachineID: id, PublicKey: public, Token: cleartext, HostFingerprint: LocalFingerprint,
		Hostname: localHostname, Address: "10.8.0.1", OS: "Debian 12", Arch: "amd64", AgentVersion: "v0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return enrolled
}

// EnrollMachine enrôle une machine distante sous l'id et le nom donnés, par
// un vrai jeton et une clé tirée pour l'occasion. Le service fournit
// l'horloge du test, l'id est fixe pour que les rendus restent figés.
func EnrollMachine(t testing.TB, machines *machine.Service, id, name string) machine.Machine {
	t.Helper()
	enrolled, _ := EnrollMachineWithKey(t, machines, id, name)
	return enrolled
}

// EnrollMachineWithKey fait comme EnrollMachine et rend aussi la clé
// privée, pour les tests qui signent comme l'agent.
func EnrollMachineWithKey(t testing.TB, machines *machine.Service, id, name string) (machine.Machine, ed25519.PrivateKey) {
	t.Helper()
	ctx := context.Background()
	cleartext, _, err := machines.CreateToken(ctx, name, nil)
	if err != nil {
		t.Fatal(err)
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	enrolled, err := machines.Enroll(ctx, machine.Enrollment{
		MachineID: id, PublicKey: public, Token: cleartext,
		Hostname: name, Address: "51.15.20.114", OS: "Debian 12", Arch: "amd64", AgentVersion: "v0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return enrolled, private
}
