package backup_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/backup"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
)

const sourceID = "99999999-2222-4333-8444-555555555555"

// agentPlan est un plan dont la source est la machine « pve ».
func agentPlan(subject, destinationID string) backup.PlanDefinition {
	return backup.PlanDefinition{
		Subject: subject, Name: "Machines virtuelles", SourceKind: backup.SourceAgent, SourceMachineID: sourceID,
		Producer: machine.ProducerProxmoxVzdump, Selector: backup.SelectorAll, DestinationID: destinationID, Keep: 5,
	}
}

// withSource enrôle la machine qui produira les sauvegardes d'un plan agent.
func (b *bench) withSource() {
	b.t.Helper()
	storetest.EnrollMachine(b.t, b.machines, sourceID, "pve")
}

// Ce qu'un plan accepte, sans rien lire de la base : la base est le seul plan
// qui s'appelle « database », une source agent a sa machine, son producteur
// et son sélecteur, et « opencloud » préfixe les fichiers de la base.
func TestPlanDefinition_Validate(t *testing.T) {
	database := databasePlan("bkd_1", 14)
	agent := agentPlan("pve-vm", "bkd_1")
	with := func(base backup.PlanDefinition, change func(*backup.PlanDefinition)) backup.PlanDefinition {
		change(&base)
		return base
	}
	cases := []struct {
		name       string
		definition backup.PlanDefinition
		want       error
	}{
		{"the database plan", database, nil},
		{"an agent plan", agent, nil},
		{"a selector of identifiers", with(agent, func(d *backup.PlanDefinition) { d.Selector = "5010,5012" }), nil},
		{"a single identifier", with(agent, func(d *backup.PlanDefinition) { d.Selector = "100" }), nil},
		{"a subject in capitals", with(agent, func(d *backup.PlanDefinition) { d.Subject = "Pve" }), backup.ErrPlanInvalid},
		{"a subject with a slash", with(agent, func(d *backup.PlanDefinition) { d.Subject = "pve/vm" }), backup.ErrPlanInvalid},
		{"a subject starting with a dash", with(agent, func(d *backup.PlanDefinition) { d.Subject = "-pve" }), backup.ErrPlanInvalid},
		{"an empty subject", with(agent, func(d *backup.PlanDefinition) { d.Subject = "" }), backup.ErrPlanInvalid},
		{"a subject of 41 characters", with(agent, func(d *backup.PlanDefinition) { d.Subject = strings.Repeat("a", 41) }), backup.ErrPlanInvalid},
		{"a subject of 40 characters", with(agent, func(d *backup.PlanDefinition) { d.Subject = strings.Repeat("a", 40) }), nil},
		{"a subject ending like the archive of a guest", with(agent, func(d *backup.PlanDefinition) { d.Subject = "pve-vm-100" }), backup.ErrPlanInvalid},
		{"a subject ending like the archive of any guest", with(agent, func(d *backup.PlanDefinition) { d.Subject = "x-vm-1" }), backup.ErrPlanInvalid},
		{"a subject ending with vm", with(agent, func(d *backup.PlanDefinition) { d.Subject = "pve-vm" }), nil},
		{"a subject with vm and digits inside", with(agent, func(d *backup.PlanDefinition) { d.Subject = "pve-vm-100-hebdo" }), nil},
		{"a subject vm and digits without a dash before", with(agent, func(d *backup.PlanDefinition) { d.Subject = "vm-100" }), nil},
		{"the database named after an agent", with(database, func(d *backup.PlanDefinition) { d.Subject = "pve" }), backup.ErrPlanInvalid},
		{"an agent plan called database", with(agent, func(d *backup.PlanDefinition) { d.Subject = "database" }), backup.ErrPlanInvalid},
		{"an agent plan taking the prefix of the database", with(agent, func(d *backup.PlanDefinition) { d.Subject = "opencloud" }), backup.ErrPlanInvalid},
		{"the database with a machine", with(database, func(d *backup.PlanDefinition) { d.SourceMachineID = sourceID }), backup.ErrPlanInvalid},
		{"the database with a producer", with(database, func(d *backup.PlanDefinition) { d.Producer = machine.ProducerProxmoxVzdump }), backup.ErrPlanInvalid},
		{"the database with a selector", with(database, func(d *backup.PlanDefinition) { d.Selector = backup.SelectorAll }), backup.ErrPlanInvalid},
		{"a source of another kind", with(agent, func(d *backup.PlanDefinition) { d.SourceKind = "nfs" }), backup.ErrPlanInvalid},
		{"no source", with(agent, func(d *backup.PlanDefinition) { d.SourceKind = "" }), backup.ErrPlanInvalid},
		{"a name of 81 characters", with(agent, func(d *backup.PlanDefinition) { d.Name = strings.Repeat("é", 81) }), backup.ErrPlanInvalid},
		{"a name of 80 characters", with(agent, func(d *backup.PlanDefinition) { d.Name = strings.Repeat("é", 80) }), nil},
		{"a name on two lines", with(agent, func(d *backup.PlanDefinition) { d.Name = "deux\nlignes" }), backup.ErrPlanInvalid},
		{"no machine", with(agent, func(d *backup.PlanDefinition) { d.SourceMachineID = "" }), backup.ErrSourceMachineUnknown},
		{"the test producer", with(agent, func(d *backup.PlanDefinition) { d.Producer = "test-echo" }), backup.ErrProducerUnknown},
		{"no producer", with(agent, func(d *backup.PlanDefinition) { d.Producer = "" }), backup.ErrProducerUnknown},
		{"no selector", with(agent, func(d *backup.PlanDefinition) { d.Selector = "" }), backup.ErrSelectorInvalid},
		{"a selector with a space", with(agent, func(d *backup.PlanDefinition) { d.Selector = "100, 101" }), backup.ErrSelectorInvalid},
		{"a selector ending with a comma", with(agent, func(d *backup.PlanDefinition) { d.Selector = "100," }), backup.ErrSelectorInvalid},
		{"a selector of words", with(agent, func(d *backup.PlanDefinition) { d.Selector = "pve" }), backup.ErrSelectorInvalid},
		{"an identifier of ten digits", with(agent, func(d *backup.PlanDefinition) { d.Selector = "1234567890" }), backup.ErrSelectorInvalid},
		{"a selector too long", with(agent, func(d *backup.PlanDefinition) { d.Selector = strings.Repeat("1,", 128) + "1" }), backup.ErrSelectorInvalid},
		{"a keep of 366", with(agent, func(d *backup.PlanDefinition) { d.Keep = 366 }), backup.ErrKeepInvalid},
		{"a negative keep", with(agent, func(d *backup.PlanDefinition) { d.Keep = -1 }), backup.ErrKeepInvalid},
	}
	for _, c := range cases {
		definition := c.definition
		definition.Complete()
		err := definition.Validate()
		if c.want == nil && err != nil || c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
		if c.want != nil {
			if _, isRefusal := refusal.KeyOf(err); !isRefusal {
				t.Errorf("%s: %v is not a refusal", c.name, err)
			}
		}
	}
}

// Un plan agent se crée et s'affiche, avec le nom de sa machine, après la
// base ; « Sauvegarder maintenant » le refuse tant que l'ordre qui produit
// n'existe pas, et la page le sait par la clé écrite dans le plan.
func TestSavePlan_AnAgentPlanIsStoredButCannotRunYet(t *testing.T) {
	b := newBench(t, rights)
	b.withSource()
	_, destination := b.ready()
	saved, err := b.backups.SavePlan(operator(), agentPlan("pve-vm", destination.ID))
	if err != nil {
		t.Fatal(err)
	}
	if saved.SourceMachineID != sourceID || saved.SourceMachineName != "pve" || saved.Producer != machine.ProducerProxmoxVzdump ||
		saved.Selector != backup.SelectorAll || saved.Name != "Machines virtuelles" || saved.Keep != 5 {
		t.Fatalf("saved %+v", saved)
	}
	overview, err := b.backups.Overview(context.Background())
	if err != nil || len(overview.Plans) != 2 || overview.Plans[0].Subject != backup.SubjectDatabase || overview.Plans[1].Subject != "pve-vm" {
		t.Fatalf("overview %+v %v", overview.Plans, err)
	}

	_, err = b.backups.RunNow(operator(), "pve-vm")
	requireRefusal(t, err, backup.ErrSourceNotImplemented)
	plan, err := b.db.GetBackupPlan(context.Background(), "pve-vm")
	if err != nil || plan.LastError != "backup.source_not_implemented" || plan.LastRunID != "" || !plan.LastAttemptAt.Equal(benchNow) {
		t.Fatalf("plan %+v %v", plan, err)
	}
	if len(b.commands.orders) != 0 {
		t.Fatalf("orders were sent: %+v", b.commands.kinds)
	}
	if entries, _ := os.ReadDir(b.stateDir.Name() + "/outgoing"); len(entries) != 0 {
		t.Fatalf("a parcel was left: %v", entries)
	}
	// Le plan de la base n'a pas été touché par le refus de l'autre.
	if database, _ := b.db.GetBackupPlan(context.Background(), backup.SubjectDatabase); database.LastError != "" || !database.LastAttemptAt.IsZero() {
		t.Fatalf("database plan %+v", database)
	}

	events, _ := b.db.ListAuditEvents(context.Background(), audit.Query{Limit: 20, ObjectKind: audit.ObjectBackupPlan})
	var found bool
	for _, event := range events {
		if event.Action == audit.BackupPlanUpdate && event.ObjectName == "pve-vm" {
			found = true
			if event.Details["source_kind"] != "agent" || event.Details["source_machine_id"] != sourceID ||
				event.Details["producer"] != machine.ProducerProxmoxVzdump || event.Details["selector"] != "all" {
				t.Errorf("details %v", event.Details)
			}
		}
	}
	if !found {
		t.Errorf("no update of pve-vm in %+v", events)
	}
}

func TestSavePlan_ChecksTheSourceMachine(t *testing.T) {
	b := newBench(t, rights)
	_, destination := b.ready()
	unknown := agentPlan("pve-vm", destination.ID)
	_, err := b.backups.SavePlan(operator(), unknown)
	requireRefusal(t, err, backup.ErrSourceMachineUnknown)
	if plans, _ := b.db.ListBackupPlans(context.Background()); len(plans) != 1 {
		t.Fatalf("a refused plan was written: %+v", plans)
	}
}

// Deux plans identiques (même source, même destination) feraient deux fois la
// même chose ; la même source vers une autre destination, c'est une deuxième
// copie, et se permet.
func TestSavePlan_RefusesATwinButAllowsASecondCopy(t *testing.T) {
	b := newBench(t, rights)
	b.withSource()
	_, first := b.ready()
	second, err := b.backups.CreateDestination(operator(), backup.DestinationDefinition{Name: "store-2", MachineID: machineID, Path: "/srv/backups/deuxieme"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.backups.SavePlan(operator(), agentPlan("pve-vm", first.ID)); err != nil {
		t.Fatal(err)
	}
	_, err = b.backups.SavePlan(operator(), agentPlan("pve-vm-bis", first.ID))
	requireRefusal(t, err, backup.ErrPlanExists)
	if !refusal.IsConflict(err) {
		t.Fatal("a twin is a conflict")
	}
	// Réécrire le plan lui-même n'est pas un doublon.
	if _, err := b.backups.SavePlan(operator(), agentPlan("pve-vm", first.ID)); err != nil {
		t.Fatalf("saving the plan again: %v", err)
	}
	if _, err := b.backups.SavePlan(operator(), agentPlan("pve-vm-bis", second.ID)); err != nil {
		t.Fatalf("a second copy: %v", err)
	}
	other := agentPlan("pve-ct", first.ID)
	other.Selector = "200"
	if _, err := b.backups.SavePlan(operator(), other); err != nil {
		t.Fatalf("another selector: %v", err)
	}
}

// Refaire un plan change ce que l'opérateur règle, pas son histoire ni son
// identité.
func TestSavePlan_ChangesAnAgentPlanWithoutLosingItsHistory(t *testing.T) {
	b := newBench(t, rights)
	b.withSource()
	_, destination := b.ready()
	first, err := b.backups.SavePlan(operator(), agentPlan("pve-vm", destination.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.backups.RunNow(operator(), "pve-vm"); err == nil {
		t.Fatal("an agent plan cannot run yet")
	}
	changed := agentPlan("pve-vm", destination.ID)
	changed.Name, changed.Selector, changed.Keep = "", "5010,5012", 30
	saved, err := b.backups.SavePlan(operator(), changed)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID != first.ID || saved.Name != "" || saved.Selector != "5010,5012" || saved.Keep != 30 ||
		saved.LastError != "backup.source_not_implemented" || saved.LastAttemptAt.IsZero() || !saved.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("saved %+v, first %+v", saved, first)
	}
}

// La nuit lance les plans dus l'un après l'autre : le refus de l'un n'arrête
// pas les autres, et chacun note sa tentative.
func TestRunIfDue_RunsEachDuePlanOnItsOwn(t *testing.T) {
	b := newBench(t, rights)
	b.withSource()
	_, destination := b.ready()
	if _, err := b.backups.SavePlan(operator(), agentPlan("pve-vm", destination.ID)); err != nil {
		t.Fatal(err)
	}
	// « pve-ct » passe avant « pve-vm » : son refus ne doit pas arrêter la suite.
	containers := agentPlan("pve-ct", destination.ID)
	containers.Selector = "200"
	if _, err := b.backups.SavePlan(operator(), containers); err != nil {
		t.Fatal(err)
	}
	b.now = time.Date(2026, 10, 22, 3, 5, 0, 0, time.UTC)
	b.backups.RunIfDue(context.Background())
	// La base part (réception et élagage) ; les plans agent sont refusés.
	if len(b.commands.orders) != 2 {
		t.Fatalf("orders %+v", b.commands.kinds)
	}
	database, _ := b.db.GetBackupPlan(context.Background(), backup.SubjectDatabase)
	if database.LastRunID == "" || database.LastError != "" || !database.LastAttemptAt.Equal(b.now) {
		t.Fatalf("database %+v", database)
	}
	for _, subject := range []string{"pve-ct", "pve-vm"} {
		agent, _ := b.db.GetBackupPlan(context.Background(), subject)
		if agent.LastError != "backup.source_not_implemented" || !agent.LastAttemptAt.Equal(b.now) {
			t.Fatalf("%s %+v", subject, agent)
		}
	}
	// Une fois par nuit et par plan.
	b.now = b.now.Add(15 * time.Minute)
	b.backups.RunIfDue(context.Background())
	if len(b.commands.orders) != 2 {
		t.Fatalf("a second run the same night: %d orders", len(b.commands.orders))
	}
}

func TestDeletePlan_StopsOnlyTheOneItNames(t *testing.T) {
	b := newBench(t, rights)
	b.withSource()
	_, destination := b.ready()
	if _, err := b.backups.SavePlan(operator(), agentPlan("pve-vm", destination.ID)); err != nil {
		t.Fatal(err)
	}
	if err := b.backups.DeletePlan(operator(), "pve-vm"); err != nil {
		t.Fatal(err)
	}
	if err := b.backups.DeletePlan(operator(), "pve-vm"); !errors.Is(err, backup.ErrNotFound) {
		t.Fatalf("twice: %v", err)
	}
	if _, err := b.db.GetBackupPlan(context.Background(), backup.SubjectDatabase); err != nil {
		t.Fatalf("the database plan went with it: %v", err)
	}
	_, err := b.backups.RunNow(operator(), "pve-vm")
	requireRefusal(t, err, backup.ErrPlanMissing)
}

// La machine qui produit les sauvegardes d'un plan est retenue comme celle
// qui les reçoit : le plan d'abord.
func TestRemoveMachine_IsHeldBackByThePlanItProduces(t *testing.T) {
	b := newBench(t, rights)
	b.withSource()
	_, destination := b.ready()
	if _, err := b.backups.SavePlan(operator(), agentPlan("pve-vm", destination.ID)); err != nil {
		t.Fatal(err)
	}
	if err := b.machines.Remove(operator(), sourceID); !errors.Is(err, machine.ErrHasBackupPlans) {
		t.Fatalf("remove the source: %v", err)
	}
	if err := b.backups.DeletePlan(operator(), "pve-vm"); err != nil {
		t.Fatal(err)
	}
	if err := b.machines.Remove(operator(), sourceID); err != nil {
		t.Fatalf("released: %v", err)
	}
}
