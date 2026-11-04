package action_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/ldesfontaine/opencloud/internal/action"
	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/anchor"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

func produceOrder(t *testing.T, recipient, target string) []byte {
	t.Helper()
	params, err := json.Marshal(agentwire.BackupProduceParams{Producer: "proxmox-vzdump", Target: target, Prefix: "pve-vm-5012", Recipient: recipient, Parcel: "colis"})
	if err != nil {
		t.Fatal(err)
	}
	return params
}

func reportAnchors(t *testing.T, b *bench, text string) {
	t.Helper()
	found, err := b.db.GetMachine(context.Background(), machineID)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.db.RecordAnchors(context.Background(), found.Agent.ID, text); err != nil {
		t.Fatal(err)
	}
}

// Le serveur n'envoie pas un ordre que les ancres rapportées par l'agent
// feraient refuser : le refus nomme l'ancre (sa clé) et porte la commande.
func TestSend_AnOrderTheReportedAnchorsWouldRefuseIsNotSent(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	recipient := identity.Recipient().String()
	b := newBenchReporting(t, "observe,backup.produce=proxmox-vzdump")

	cases := []struct {
		name     string
		reported string
		target   string
		key      string
		command  string
	}{
		{"no anchor posed", "none", "5012", "anchor.backup_recipient_missing", "agent trust add backup.recipient " + recipient},
		{"a guest outside", "backup.recipient=" + recipient + ",backup.guest=101:102", "5012", "anchor.backup_guest_outside", "agent trust add backup.guest 5012"},
		{"unreadable guests", "backup.recipient=" + recipient + ",backup.guest=!", "5012", "anchor.backup_guest_unreadable", "agent trust add backup.guest 5012"},
	}
	for _, c := range cases {
		reportAnchors(t, b, c.reported)
		_, err := b.service.Send(operator(), machineID, agentwire.KindBackupProduce, produceOrder(t, recipient, c.target))
		var refused *anchor.Refused
		if !errors.As(err, &refused) {
			t.Fatalf("%s: %v", c.name, err)
		}
		if key, _ := refusal.KeyOf(err); key != c.key || !refusal.IsConflict(err) {
			t.Errorf("%s: key %q", c.name, key)
		}
		if !strings.HasSuffix(refused.Command(), c.command) {
			t.Errorf("%s: command %q", c.name, refused.Command())
		}
	}
	if len(b.pusher.sent) != 0 || len(b.runs(t)) != 0 {
		t.Fatalf("a refused order left: %d sent, %d runs", len(b.pusher.sent), len(b.runs(t)))
	}

	reportAnchors(t, b, "backup.recipient="+recipient+",backup.guest=5012:5013")
	if _, err := b.service.Send(operator(), machineID, agentwire.KindBackupProduce, produceOrder(t, recipient, "5012")); err != nil {
		t.Fatalf("an anchored order: %v", err)
	}
}

// Un agent d'avant les ancres ne les applique pas : le serveur n'en juge aucune.
func TestSend_AnAgentThatReportsNoAnchorIsNotJudgedByThem(t *testing.T) {
	b := newBenchReporting(t, "observe,backup.produce=proxmox-vzdump")
	if _, err := b.service.Send(operator(), machineID, agentwire.KindBackupProduce, produceOrder(t, "age1x", "5012")); err != nil {
		t.Fatalf("v0.6.0 agent: %v", err)
	}
	if _, err := b.service.Send(operator(), machineID, agentwire.KindAgentPing, nil); err != nil {
		t.Fatalf("ping: %v", err)
	}
	var kinds []action.Run
	kinds = append(kinds, b.runs(t)...)
	if len(kinds) != 2 {
		t.Fatalf("%d runs", len(kinds))
	}
}
