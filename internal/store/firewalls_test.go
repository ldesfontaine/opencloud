package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/firewall"
	"github.com/ldesfontaine/opencloud/internal/seal"
)

var firewallAt = time.Unix(1_800_000_000, 0)

const plainFirewallSecret = "s3cr3t-firewall-api-secret-for-tests" // gitleaks:allow -- faux secret de test

func firewallCredentials(id, name, url string) firewall.Credentials {
	return firewall.Credentials{
		Firewall: firewall.Firewall{
			ID: id, Name: name, Kind: firewall.KindOPNsense, APIURL: url, APIKey: "key-of-" + id,
			Pin: "ab", Version: "OPNsense 25.1", VerifiedAt: firewallAt, CreatedAt: firewallAt,
		},
		APISecret: plainFirewallSecret,
	}
}

func declareFirewall(t *testing.T, db *DB, id, name, url string) {
	t.Helper()
	if err := db.CreateFirewall(context.Background(), firewallCredentials(id, name, url)); err != nil {
		t.Fatal(err)
	}
}

func sampleSnapshot() firewall.Snapshot {
	return firewall.Snapshot{
		Version: "OPNsense 25.1.6",
		Interfaces: []firewall.Interface{
			{Name: "wan", Device: "igb0", Role: "WAN", Address: "203.0.113.2/24", Status: "up"},
			{Name: "lan", Device: "igb1", Role: "LAN", Address: "192.168.1.1/24", Status: "up"},
		},
		Rules: []firewall.Rule{
			{UUID: "u1", Interface: "lan", Action: "pass", Direction: "in", Protocol: "TCP", Source: "any", Destination: "any", DestinationPort: "443", Description: "web", Enabled: true},
			{UUID: "u2", Interface: "wan", Action: "block", Direction: "in", Protocol: "any", Source: "any", Destination: "any", Enabled: false},
		},
		Leases: []firewall.Lease{
			{Address: "192.168.1.20", MAC: "aa:bb:cc:dd:ee:ff", Hostname: "nas", Interface: "LAN", Server: "dnsmasq", ExpiresAt: firewallAt.Add(time.Hour)},
			{Address: "192.168.1.21", Server: "kea", IsStatic: true},
		},
		Gateways: []firewall.Gateway{
			{Name: "WAN_GW", Address: "203.0.113.1", Status: firewall.GatewayOnline, IsMonitored: true, DelayMillis: 1.5, LossPercent: 0},
			{Name: "BACKUP_GW", Status: firewall.GatewayUnknown},
		},
	}
}

func TestFirewall_DeclareReadAndView(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if _, err := db.GetFirewallView(ctx, "fw1"); !errors.Is(err, firewall.ErrNotFound) {
		t.Fatalf("view of an undeclared firewall: %v", err)
	}
	declareFirewall(t, db, "fw1", "Bordure", "https://fw.lan")
	view, err := db.GetFirewallView(ctx, "fw1")
	if err != nil {
		t.Fatal(err)
	}
	if view.Name != "Bordure" || view.APIKey != "key-of-fw1" || !view.LastReadAt.IsZero() || len(view.Rules) != 0 {
		t.Fatalf("firewall before any reading: %+v", view)
	}

	if err := db.ReplaceFirewallSnapshot(ctx, "fw1", sampleSnapshot(), firewallAt); err != nil {
		t.Fatal(err)
	}
	view, err = db.GetFirewallView(ctx, "fw1")
	if err != nil {
		t.Fatal(err)
	}
	if view.Version != "OPNsense 25.1.6" || !view.LastReadAt.Equal(firewallAt) {
		t.Errorf("firewall after the reading: %+v", view.Firewall)
	}
	if len(view.Rules) != 2 || view.Rules[0].UUID != "u1" || !view.Rules[0].Enabled || view.Rules[1].Enabled {
		t.Errorf("rules must keep their order and their enabled flag: %+v", view.Rules)
	}
	if len(view.Interfaces) != 2 || view.Interfaces[0].Name != "lan" {
		t.Errorf("interfaces by name: %+v", view.Interfaces)
	}
	if len(view.Leases) != 2 || !view.Leases[0].ExpiresAt.Equal(firewallAt.Add(time.Hour)) || !view.Leases[1].ExpiresAt.IsZero() || !view.Leases[1].IsStatic {
		t.Errorf("leases: %+v", view.Leases)
	}
	if len(view.Gateways) != 2 || view.Gateways[0].Name != "BACKUP_GW" || view.Gateways[0].IsMonitored ||
		!view.Gateways[1].IsMonitored || view.Gateways[1].DelayMillis != 1.5 {
		t.Errorf("gateways: %+v", view.Gateways)
	}

	summaries, err := db.ListFirewalls(ctx)
	if err != nil || len(summaries) != 1 || summaries[0].Rules != 2 || summaries[0].Leases != 2 || summaries[0].Interfaces != 2 || summaries[0].Gateways != 2 {
		t.Fatalf("summaries: %+v %v", summaries, err)
	}
}

func TestFirewall_AFailureKeepsTheLastReadingAndANewReadingReplacesIt(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	declareFirewall(t, db, "fw1", "Bordure", "https://fw.lan")
	if err := db.ReplaceFirewallSnapshot(ctx, "fw1", sampleSnapshot(), firewallAt); err != nil {
		t.Fatal(err)
	}
	if err := db.NoteFirewallFailure(ctx, "fw1", "firewall.unreachable"); err != nil {
		t.Fatal(err)
	}
	view, _ := db.GetFirewallView(ctx, "fw1")
	if view.LastError != "firewall.unreachable" || len(view.Rules) != 2 || !view.LastReadAt.Equal(firewallAt) {
		t.Fatalf("a failure must keep the last reading: %+v", view)
	}

	later := firewallAt.Add(5 * time.Minute)
	smaller := firewall.Snapshot{Version: "OPNsense 25.1.7", Rules: sampleSnapshot().Rules[:1]}
	if err := db.ReplaceFirewallSnapshot(ctx, "fw1", smaller, later); err != nil {
		t.Fatal(err)
	}
	view, _ = db.GetFirewallView(ctx, "fw1")
	if len(view.Rules) != 1 || len(view.Leases) != 0 || len(view.Interfaces) != 0 || view.LastError != "" || !view.LastReadAt.Equal(later) {
		t.Fatalf("a reading replaces everything: %+v", view)
	}

	if err := db.ReplaceFirewallSnapshot(ctx, "nope", smaller, later); !errors.Is(err, firewall.ErrNotFound) {
		t.Fatalf("reading for an undeclared firewall: %v", err)
	}
	if err := db.NoteFirewallFailure(ctx, "nope", "firewall.unreachable"); !errors.Is(err, firewall.ErrNotFound) {
		t.Fatalf("failure for an undeclared firewall: %v", err)
	}
}

func TestFirewall_ANewKeyForTheSameAddressKeepsTheReadingAnotherAddressClearsIt(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	declareFirewall(t, db, "fw1", "Bordure", "https://fw.lan")
	if err := db.ReplaceFirewallSnapshot(ctx, "fw1", sampleSnapshot(), firewallAt); err != nil {
		t.Fatal(err)
	}
	if err := db.NoteFirewallFailure(ctx, "fw1", "firewall.unauthorized"); err != nil {
		t.Fatal(err)
	}

	renewed := firewallCredentials("fw1", "Bordure", "https://fw.lan")
	renewed.APIKey = "renewed-key"
	if err := db.UpdateFirewall(ctx, renewed); err != nil {
		t.Fatal(err)
	}
	view, _ := db.GetFirewallView(ctx, "fw1")
	if view.APIKey != "renewed-key" || len(view.Rules) != 2 || view.LastError != "" || !view.LastReadAt.Equal(firewallAt) {
		t.Fatalf("the same address keeps what was read and clears the failure: %+v", view)
	}

	if err := db.UpdateFirewall(ctx, firewallCredentials("fw1", "Bordure", "https://other.lan")); err != nil {
		t.Fatal(err)
	}
	view, _ = db.GetFirewallView(ctx, "fw1")
	if len(view.Rules) != 0 || len(view.Gateways) != 0 || !view.LastReadAt.IsZero() {
		t.Fatalf("another address must not show the old firewall's reading: %+v", view)
	}
	if err := db.UpdateFirewall(ctx, firewallCredentials("nope", "X", "https://x.lan")); !errors.Is(err, firewall.ErrNotFound) {
		t.Fatalf("update of an undeclared firewall: %v", err)
	}
}

func TestFirewall_NamesAreUniqueWithoutCaseAndCapped(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	declareFirewall(t, db, "fw1", "Bordure", "https://fw.lan")
	if err := db.CreateFirewall(ctx, firewallCredentials("fw2", "bordure", "https://other.lan")); !errors.Is(err, firewall.ErrNameTaken) {
		t.Fatalf("same name, other case: %v", err)
	}
	declareFirewall(t, db, "fw2", "Autre", "https://other.lan")
	if err := db.UpdateFirewall(ctx, firewallCredentials("fw2", "BORDURE", "https://other.lan")); !errors.Is(err, firewall.ErrNameTaken) {
		t.Fatalf("renaming onto a taken name: %v", err)
	}

	for index := 3; index <= firewall.MaxFirewalls; index++ {
		declareFirewall(t, db, fmt.Sprintf("fw%d", index), fmt.Sprintf("Pare-feu %d", index), "https://fw.lan")
	}
	if err := db.CreateFirewall(ctx, firewallCredentials("over", "Trop", "https://fw.lan")); !errors.Is(err, firewall.ErrTooMany) {
		t.Fatalf("over the cap: %v", err)
	}
}

func TestFirewall_RemoveTakesTheReadingWithIt(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	declareFirewall(t, db, "fw1", "Bordure", "https://fw.lan")
	if err := db.ReplaceFirewallSnapshot(ctx, "fw1", sampleSnapshot(), firewallAt); err != nil {
		t.Fatal(err)
	}
	ids, err := db.ListFirewallIDs(ctx)
	if err != nil || !reflect.DeepEqual(ids, []string{"fw1"}) {
		t.Fatalf("ids %v, %v", ids, err)
	}
	if err := db.DeleteFirewall(ctx, "fw1"); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteFirewall(ctx, "fw1"); !errors.Is(err, firewall.ErrNotFound) {
		t.Fatalf("second removal: %v", err)
	}
	for _, table := range []string{"firewall_interfaces", "firewall_rules", "firewall_leases", "firewall_gateways"} {
		if remaining := count(t, db, `SELECT COUNT(*) FROM `+table); remaining != 0 {
			t.Errorf("%s keeps %d rows after the firewall is removed", table, remaining)
		}
	}
}

func TestFirewall_TheSecretIsSealedBoundToItsRowAndForgotten(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	declareFirewall(t, db, "fw1", "Bordure", "https://fw.lan")
	stored := rawColumn(t, db, `SELECT api_secret FROM firewalls`)
	if stored == plainFirewallSecret || !seal.IsSealed(stored) {
		t.Fatalf("the secret is not sealed: %q", stored)
	}
	credentials, err := db.GetFirewallCredentials(ctx, "fw1")
	if err != nil || credentials.APISecret != plainFirewallSecret {
		t.Fatalf("credentials: %v", err)
	}

	// Le même scellé recopié sur une autre ligne ne s'ouvre pas.
	exec(t, db, `INSERT INTO firewalls (id, name, kind, api_url, api_key, api_secret, verified_at, created_at)
		VALUES ('copy', 'Copie', 'opnsense', 'https://x.lan', 'k', ?, 1, 1)`, stored)
	if _, err := db.GetFirewallCredentials(ctx, "copy"); !errors.Is(err, ErrSecretUnreadable) {
		t.Fatalf("a sealed secret copied to another row: %v", err)
	}

	forgotten, err := db.ForgetSecrets(ctx, func() (string, error) { return "renewed", nil })
	if err != nil || forgotten.Firewalls != 2 {
		t.Fatalf("forgotten: %+v %v", forgotten, err)
	}
	credentials, err = db.GetFirewallCredentials(ctx, "fw1")
	if err != nil || credentials.APISecret != "" {
		t.Fatalf("a forgotten secret must read empty: %q %v", credentials.APISecret, err)
	}
}
