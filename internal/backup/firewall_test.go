package backup_test

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/backup"
	"github.com/ldesfontaine/opencloud/internal/firewall"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/store/storetest"
)

const (
	benchFirewallID = "fw0123456789abcdef"
	// Une valeur secrète semée dans le config.xml du faux pare-feu.
	configurationSecret = "seeded-config-secret-77c1"
)

// fakeFirewalls est le module Pare-feu vu de la sauvegarde.
type fakeFirewalls struct {
	err   error
	reads int
}

func (f *fakeFirewalls) Declared(context.Context) ([]firewall.Summary, error) {
	return []firewall.Summary{{Firewall: firewall.Firewall{ID: benchFirewallID, Name: "Bordure"}}}, nil
}

func (f *fakeFirewalls) ExportConfiguration(_ context.Context, id string) ([]byte, error) {
	f.reads++
	if f.err != nil {
		return nil, f.err
	}
	if id != benchFirewallID {
		return nil, firewall.ErrNotFound
	}
	return []byte("<opnsense><password>" + configurationSecret + "</password></opnsense>"), nil
}

func firewallPlan(destinationID string) backup.PlanDefinition {
	return backup.PlanDefinition{
		Subject: "bordure", SourceKind: backup.SourceAgent, Producer: backup.ProducerOPNsense,
		Selector: benchFirewallID, DestinationID: destinationID, Keep: 5,
	}
}

func firewallBench(t *testing.T) (*bench, *fakeFirewalls, backup.Keyring, backup.Destination) {
	t.Helper()
	b := newBench(t, rights)
	storetest.SaveLocalMachine(t, b.db, benchNow)
	fake := &fakeFirewalls{}
	b.backups.SetFirewalls(fake)
	keyring, destination := b.ready()
	return b, fake, keyring, destination
}

func TestSavePlan_FirewallExportStartsFromTheOpenCloudMachine(t *testing.T) {
	b, _, _, destination := firewallBench(t)
	plan, err := b.backups.SavePlan(operator(), firewallPlan(destination.ID))
	if err != nil {
		t.Fatal(err)
	}
	if plan.SourceMachineID != machine.LocalID || !plan.IsFirewallExport() {
		t.Fatalf("plan %+v", plan)
	}
}

func TestSavePlan_FirewallExportRefusals(t *testing.T) {
	b, fake, _, destination := firewallBench(t)
	other := firewallPlan(destination.ID)
	other.Selector = "unknown"
	_, err := b.backups.SavePlan(operator(), other)
	requireRefusal(t, err, backup.ErrFirewallUnknown)

	elsewhere := firewallPlan(destination.ID)
	elsewhere.SourceMachineID = machineID
	_, err = b.backups.SavePlan(operator(), elsewhere)
	requireRefusal(t, err, backup.ErrSourceMachineUnknown)

	all := firewallPlan(destination.ID)
	all.Selector = backup.SelectorAll
	_, err = b.backups.SavePlan(operator(), all)
	requireRefusal(t, err, backup.ErrSelectorInvalid)

	// Le droit manquant se dit à la création, avec son libellé exact.
	fake.err = firewall.ErrExportForbidden
	_, err = b.backups.SavePlan(operator(), firewallPlan(destination.ID))
	requireRefusal(t, err, firewall.ErrExportForbidden)
	if !strings.Contains(err.Error(), "Diagnostics: Configuration History") {
		t.Errorf("the refusal does not name the privilege: %v", err)
	}
	if _, err := b.db.GetBackupPlan(context.Background(), "bordure"); !errors.Is(err, backup.ErrNotFound) {
		t.Fatalf("a refused plan was written: %v", err)
	}

	// Une fois créé, changer le nombre gardé ne relit pas le pare-feu.
	fake.err = nil
	if _, err := b.backups.SavePlan(operator(), firewallPlan(destination.ID)); err != nil {
		t.Fatal(err)
	}
	reads := fake.reads
	edited := firewallPlan(destination.ID)
	edited.Keep = 9
	if _, err := b.backups.SavePlan(operator(), edited); err != nil || fake.reads != reads {
		t.Fatalf("edit: %v, reads %d -> %d", err, reads, fake.reads)
	}
}

func TestRun_FirewallExportSendsAnEncryptedConfigurationAndNothingInTheClear(t *testing.T) {
	b, _, keyring, destination := firewallBench(t)
	if _, err := b.backups.SavePlan(operator(), firewallPlan(destination.ID)); err != nil {
		t.Fatal(err)
	}
	run, err := b.backups.RunNow(operator(), "bordure")
	if err != nil {
		t.Fatal(err)
	}
	if run.Kind != agentwire.KindBackupReceive || len(b.commands.orders) != 2 || b.commands.kinds[1] != agentwire.KindBackupPrune {
		t.Fatalf("run %+v orders %v", run, b.commands.kinds)
	}
	var receive agentwire.BackupReceiveParams
	if err := json.Unmarshal(b.commands.orders[0].Params, &receive); err != nil {
		t.Fatal(err)
	}
	var prune agentwire.BackupPruneParams
	if err := json.Unmarshal(b.commands.orders[1].Params, &prune); err != nil || prune.Prefix != "bordure" || prune.Keep != 5 {
		t.Fatalf("prune %+v %v", prune, err)
	}
	archiveName := agentwire.BackupFileName("bordure", benchNow)
	archive := downloaded(t, b.outbox, receive, archiveName)
	if bytes.Contains(archive, []byte(configurationSecret)) {
		t.Fatal("the configuration travels in the clear")
	}
	identity, err := backup.OpenIdentity(downloaded(t, b.outbox, receive, agentwire.BackupKeyFileName(keyring.Fingerprint)), testPassphrase)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := age.Decrypt(bytes.NewReader(archive), identity)
	if err != nil {
		t.Fatal(err)
	}
	unpacker := tar.NewReader(plain)
	header, err := unpacker.Next()
	if err != nil || header.Name != "config.xml" {
		t.Fatalf("entry %+v %v", header, err)
	}
	content, _ := io.ReadAll(unpacker)
	if !strings.Contains(string(content), configurationSecret) {
		t.Errorf("config.xml %q", content)
	}
	if left, _ := os.ReadDir(b.stateDir.Name() + "/outgoing/" + receive.Parcel); len(left) != 2 {
		t.Fatalf("parcel holds %d files, want the archive and the sealed key", len(left))
	}
	if strings.Contains(b.logs.String(), configurationSecret) {
		t.Error("the configuration reached the log")
	}
}

func TestRun_FirewallExportWhoseKeyLostThePrivilegeNotesTheRefusal(t *testing.T) {
	b, fake, _, destination := firewallBench(t)
	if _, err := b.backups.SavePlan(operator(), firewallPlan(destination.ID)); err != nil {
		t.Fatal(err)
	}
	fake.err = firewall.ErrExportForbidden
	if _, err := b.backups.RunNow(operator(), "bordure"); !errors.Is(err, firewall.ErrExportForbidden) {
		t.Fatalf("got %v", err)
	}
	plan, _ := b.db.GetBackupPlan(context.Background(), "bordure")
	if plan.LastError != "firewall.export_forbidden" || len(b.commands.orders) != 0 {
		t.Fatalf("plan %+v, orders %v", plan, b.commands.kinds)
	}
	if left, _ := os.ReadDir(b.stateDir.Name() + "/outgoing"); len(left) != 0 {
		t.Fatalf("a parcel was left: %v", left)
	}
}

func TestInventory_ADeclaredFirewallIsASourceCoveredByItsPlan(t *testing.T) {
	b, _, _, destination := firewallBench(t)
	inventory, err := b.backups.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	source := sourceByKey(t, inventory, backup.FirewallSourceKey(benchFirewallID))
	if source.Kind != backup.KindFirewall || source.Name != "Bordure" || len(source.CoveredBy) != 0 {
		t.Fatalf("source %+v", source)
	}
	if _, err := b.backups.SavePlan(operator(), firewallPlan(destination.ID)); err != nil {
		t.Fatal(err)
	}
	inventory, _ = b.backups.Inventory(context.Background())
	if source := sourceByKey(t, inventory, backup.FirewallSourceKey(benchFirewallID)); len(source.CoveredBy) != 1 || source.CoveredBy[0].Subject != "bordure" {
		t.Fatalf("coverage %+v", source.CoveredBy)
	}
}
