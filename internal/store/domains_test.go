package store

import (
	"context"
	"testing"

	"github.com/ldesfontaine/opencloud/migrations"
)

func publishedDomain() Domain {
	return Domain{
		Name:        "temoin.exemple.fr",
		MachineID:   LocalMachineID,
		Environment: "prod",
		Service:     "temoin",
		Port:        80,
	}
}

func TestDomains_FreshDatabase_HoldsNone(t *testing.T) {
	database := openTestStore(t, openTestRoot(t), migrations.Files)

	domains, err := database.Domains(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(domains) != 0 {
		t.Fatalf("domaines = %v, attendu aucun", domains)
	}
}

func TestRecordDomain_ReadsBackWhatWasPublished(t *testing.T) {
	database := openTestStore(t, openTestRoot(t), migrations.Files)
	ctx := context.Background()

	if err := database.RecordDomain(ctx, publishedDomain()); err != nil {
		t.Fatal(err)
	}

	domains, err := database.Domains(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(domains) != 1 {
		t.Fatalf("domaines = %v, attendu un", domains)
	}
	read := domains[0]
	if read.Name != "temoin.exemple.fr" || read.MachineID != LocalMachineID {
		t.Errorf("domaine = %+v", read)
	}
	if read.Environment != "prod" || read.Service != "temoin" || read.Port != 80 {
		t.Errorf("domaine = %+v", read)
	}
	if read.CreatedAt.IsZero() || read.UpdatedAt.IsZero() {
		t.Errorf("les dates doivent être lues : %+v", read)
	}
}

// Une publication rejouée met la ligne à jour ; elle n'en crée pas une
// seconde, et la date de première publication ne bouge pas.
func TestRecordDomain_Replayed_UpdatesTheLineAndKeepsItsFirstDate(t *testing.T) {
	database := openTestStore(t, openTestRoot(t), migrations.Files)
	ctx := context.Background()

	if err := database.RecordDomain(ctx, publishedDomain()); err != nil {
		t.Fatal(err)
	}
	first, err := database.Domains(ctx)
	if err != nil {
		t.Fatal(err)
	}

	moved := publishedDomain()
	moved.Port = 8080
	if err := database.RecordDomain(ctx, moved); err != nil {
		t.Fatal(err)
	}

	domains, err := database.Domains(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(domains) != 1 {
		t.Fatalf("domaines = %v, attendu un seul", domains)
	}
	if domains[0].Port != 8080 {
		t.Errorf("port = %d, attendu 8080", domains[0].Port)
	}
	if !domains[0].CreatedAt.Equal(first[0].CreatedAt) {
		t.Errorf("la date de publication a bougé : %s puis %s", first[0].CreatedAt, domains[0].CreatedAt)
	}
}

// Retirer un nom qu'on ne portait pas n'est pas une erreur : la suppression
// rejouée dit la même chose que la première.
func TestForgetDomain_RemovesTheLineAndAcceptsBeingReplayed(t *testing.T) {
	database := openTestStore(t, openTestRoot(t), migrations.Files)
	ctx := context.Background()

	if err := database.RecordDomain(ctx, publishedDomain()); err != nil {
		t.Fatal(err)
	}
	if err := database.ForgetDomain(ctx, "temoin.exemple.fr"); err != nil {
		t.Fatal(err)
	}
	if err := database.ForgetDomain(ctx, "temoin.exemple.fr"); err != nil {
		t.Fatalf("une suppression rejouée ne doit rien casser : %v", err)
	}

	domains, err := database.Domains(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(domains) != 0 {
		t.Fatalf("domaines = %v, attendu aucun", domains)
	}
}
