package agentwire

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Une sauvegarde est un préfixe, un instant et « .tar.age » : une clé
// déposée, un fichier en cours d'écriture ou un chemin ne sont jamais pris.
func TestIsBackupFile_OnlyTheExactForm(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"opencloud-20261021-030000.tar.age", true},
		{"opencloud-key-0123456789abcdef.age", false},
		{".opencloud-20261021-030000.tar.age.part", false},
		{"opencloud-20261021-030000.tar.age.part", false},
		{"pve-vm-5010-20261021-030000.tar.age", true},
		{"-20261021-030000.tar.age", false},
		{"Pve-20261021-030000.tar.age", false},
		{"20261021-030000.tar.age", false},
		{"opencloud-20261021-030000.tar", false},
		{"opencloud-2026102-030000.tar.age", false},
		{"/srv/opencloud-20261021-030000.tar.age", false},
		{"../opencloud-20261021-030000.tar.age", false},
		{"opencloud-20261021-030000.tar.age\n", false},
		{"", false},
	}
	for _, c := range cases {
		if got := IsBackupFile(c.name); got != c.want {
			t.Errorf("IsBackupFile(%q) = %t, want %t", c.name, got, c.want)
		}
	}
}

func TestIsBackupKeyFile_OnlyTheExactForm(t *testing.T) {
	for name, want := range map[string]bool{
		"opencloud-key-0123456789abcdef.age":      true,
		"opencloud-key-0123456789ABCDEF.age":      false,
		"opencloud-key-0123456789abcde.age":       false,
		"opencloud-key-0123456789abcdef.age.part": false,
		"opencloud-20261021-030000.tar.age":       false,
		"../opencloud-key-0123456789abcdef.age":   false,
	} {
		if got := IsBackupKeyFile(name); got != want {
			t.Errorf("IsBackupKeyFile(%q) = %t, want %t", name, got, want)
		}
	}
}

func TestBackupFileName_IsUTCAndSortsByDate(t *testing.T) {
	paris := time.FixedZone("CEST", 2*3600)
	earlier := BackupFileName("database", time.Date(2026, 10, 21, 3, 0, 0, 0, paris))
	later := BackupFileName("database", time.Date(2026, 10, 22, 3, 0, 0, 0, paris))
	if earlier != "opencloud-20261021-010000.tar.age" || !IsBackupFile(earlier) {
		t.Fatalf("got %q", earlier)
	}
	if earlier >= later {
		t.Errorf("%q must sort before %q", earlier, later)
	}
	if key := BackupKeyFileName("0123456789abcdef"); key != "opencloud-key-0123456789abcdef.age" || IsBackupFile(key) {
		t.Errorf("key file %q", key)
	}
}

// La base garde le nom d'avant les plans multiples ; un autre plan prend son
// sujet, suivi du suffixe de sa source quand elle en a un.
func TestBackupFilePrefix_TheDatabaseKeepsItsOldName(t *testing.T) {
	for _, c := range []struct{ subject, suffix, want string }{
		{"database", "", "opencloud"},
		{"pve", "", "pve"},
		{"pve", "vm-5010", "pve-vm-5010"},
	} {
		if got := BackupFilePrefix(c.subject, c.suffix); got != c.want {
			t.Errorf("BackupFilePrefix(%q, %q) = %q, want %q", c.subject, c.suffix, got, c.want)
		}
	}
	at := time.Date(2026, 10, 21, 3, 0, 0, 0, time.UTC)
	if got := BackupFileName("pve", at); got != "pve-20261021-030000.tar.age" || !IsBackupFile(got) {
		t.Errorf("got %q", got)
	}
}

// Un préfixe ne prend que ses fichiers : ce qui suit est exactement l'instant,
// si bien que « pve » ne prend jamais ceux de « pve-vm-100 », ni « opencloud »
// la clé déposée.
func TestIsBackupFileOf_TheRestIsExactlyTheInstant(t *testing.T) {
	cases := []struct {
		name, prefix string
		want         bool
	}{
		{"opencloud-20261021-030000.tar.age", "opencloud", true},
		{"pve-20261021-030000.tar.age", "pve", true},
		{"pve-vm-100-20261021-030000.tar.age", "pve", false},
		{"pve-vm-100-20261021-030000.tar.age", "pve-vm-100", true},
		{"pve-20261021-030000-20261021-030000.tar.age", "pve", false},
		{"pve-20261021-030000-20261021-030000.tar.age", "pve-20261021-030000", true},
		{"pve2-20261021-030000.tar.age", "pve", false},
		{"opencloud-key-0123456789abcdef.age", "opencloud", false},
		{"pve-20261021-030000.tar.age.part", "pve", false},
		{"pve-20261021-030000.tar.age", "", false},
	}
	for _, c := range cases {
		if got := IsBackupFileOf(c.name, c.prefix); got != c.want {
			t.Errorf("IsBackupFileOf(%q, %q) = %t, want %t", c.name, c.prefix, got, c.want)
		}
	}
}

func TestIsBackupPrefix_AWordMadeOfLowercaseDigitsAndDashes(t *testing.T) {
	for prefix, want := range map[string]bool{
		"opencloud":             true,
		"pve-vm-5010":           true,
		"":                      false,
		"-pve":                  false,
		"Pve":                   false,
		"pve/vm":                false,
		"pve vm":                false,
		"../pve":                false,
		strings.Repeat("a", 64): true,
		strings.Repeat("a", 65): false,
		"pve\n":                 false,
	} {
		if got := IsBackupPrefix(prefix); got != want {
			t.Errorf("IsBackupPrefix(%q) = %t, want %t", prefix, got, want)
		}
	}
}

// Le secret est signé avec l'ordre : il traverse l'enveloppe octet pour
// octet, et un ordre sans secret ne porte pas le champ.
func TestOrderBody_SecretRoundTrips(t *testing.T) {
	secret := json.RawMessage(`{"env":"une valeur de test qui ne doit jamais sortir"}`) // gitleaks:allow
	encoded, err := json.Marshal(OrderBody{RunID: "r", Kind: KindAgentPing, Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	var decoded OrderBody
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if string(decoded.Secret) != string(secret) {
		t.Errorf("secret %s, want %s", decoded.Secret, secret)
	}
	plain, err := json.Marshal(OrderBody{RunID: "r", Kind: KindAgentPing})
	if err != nil || strings.Contains(string(plain), "secret") {
		t.Errorf("an order without a secret must not carry the field: %s %v", plain, err)
	}
}

func TestParcelPath_EscapesWhatIsNotAPathSegment(t *testing.T) {
	if got := ParcelPath("abc", "opencloud-20261021-030000.tar.age"); got != "/agent/parcels/abc/opencloud-20261021-030000.tar.age" {
		t.Errorf("got %q", got)
	}
	if got := ParcelPath("a/b", "x y"); got != "/agent/parcels/a%2Fb/x%20y" {
		t.Errorf("got %q", got)
	}
}
