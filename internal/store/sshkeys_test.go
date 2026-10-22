package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/deletion"
	"github.com/ldesfontaine/opencloud/internal/sshkeys"
)

const sshFingerprint = "SHA256:nThbg6kXUpJWGl7E1IGOCspRomTxdCARLviKw6E5SY8"

func sshReport(fingerprints ...string) sshkeys.Report {
	var keys []sshkeys.Key
	for _, fingerprint := range fingerprints {
		keys = append(keys, sshkeys.Key{File: "authorized_keys", Fingerprint: fingerprint, Type: "ssh-ed25519", Bits: 256, Options: []string{}})
	}
	return sshkeys.Report{Accounts: []sshkeys.Account{{Name: "root", State: sshkeys.StateRead, Keys: keys}}}
}

// Les clés relevées partent avec la machine, et le plan de suppression les
// compte.
func TestSSHKeys_GoWithTheMachineAndTheDeletionPlanCountsThem(t *testing.T) {
	ctx := context.Background()
	db := deletionBench(t)
	other := "SHA256:" + "Qa1b2c3d4e5f6g7h8i9j0KLMNOPQRSTUVWXYZabcdef"
	if err := db.ReplaceSSHKeys(ctx, "m1", sshReport(sshFingerprint, other), time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}

	plan, err := db.MachineDeletionPlan(ctx, "m1")
	if err != nil {
		t.Fatal(err)
	}
	want := []deletion.Count{{Kind: deletion.KindSSHKeys, Count: 2}}
	if !reflect.DeepEqual(plan.Removes, want) {
		t.Fatalf("part avec : %+v", plan.Removes)
	}

	if err := db.DeleteMachine(ctx, "m1"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"ssh_reports", "ssh_accounts", "ssh_keys"} {
		if got := count(t, db, `SELECT COUNT(*) FROM `+table); got != 0 {
			t.Errorf("%s : %d ligne(s) survivent à la machine", table, got)
		}
	}
}

// Une machine sans clé relevée n'ajoute rien à son plan.
func TestSSHKeys_AMachineWithoutKeysAddsNothingToItsPlan(t *testing.T) {
	ctx := context.Background()
	db := deletionBench(t)
	if err := db.ReplaceSSHKeys(ctx, "m1", sshReport(), time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	plan, err := db.MachineDeletionPlan(ctx, "m1")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Removes) != 0 {
		t.Fatalf("part avec : %+v", plan.Removes)
	}
}

// Le schéma garde ce que le serveur valide : une clé sans compte, un fichier
// ou un état hors liste sont refusés par la base elle-même.
func TestSSHKeys_TheSchemaRefusesWhatTheServerWouldRefuse(t *testing.T) {
	db := deletionBench(t)
	exec(t, db, `INSERT INTO ssh_accounts (machine_id, account, uid, state) VALUES ('m1', 'root', 0, 'read')`)
	refused := map[string]string{
		"une clé sans compte":        `INSERT INTO ssh_keys (machine_id, account, file, fingerprint, key_type) VALUES ('m1', 'fantome', 'authorized_keys', 'f', 'ssh-ed25519')`,
		"un fichier hors liste":      `INSERT INTO ssh_keys (machine_id, account, file, fingerprint, key_type) VALUES ('m1', 'root', '../../etc/shadow', 'f', 'ssh-ed25519')`,
		"un état hors liste":         `INSERT INTO ssh_accounts (machine_id, account, uid, state) VALUES ('m1', 'lucas', 1000, 'hacked')`,
		"un uid négatif":             `INSERT INTO ssh_accounts (machine_id, account, uid, state) VALUES ('m1', 'lucas', -1, 'none')`,
		"un compte sans machine":     `INSERT INTO ssh_accounts (machine_id, account, uid, state) VALUES ('fantome', 'lucas', 1000, 'none')`,
		"un rapport sans machine":    `INSERT INTO ssh_reports (machine_id, reported_at) VALUES ('fantome', 1)`,
		"un compte en double":        `INSERT INTO ssh_accounts (machine_id, account, uid, state) VALUES ('m1', 'root', 0, 'none')`,
		"un drapeau tronqué non 0/1": `INSERT INTO ssh_reports (machine_id, truncated, reported_at) VALUES ('m1', 2, 1)`,
	}
	for name, statement := range refused {
		if _, err := db.sql.Exec(statement); err == nil {
			t.Errorf("%s : acceptée", name)
		}
	}
}
