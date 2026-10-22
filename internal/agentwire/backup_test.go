package agentwire

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// L'élagage n'efface que la forme exacte : une clé déposée, un fichier en
// cours d'écriture, un autre préfixe ou un chemin ne sont jamais pris.
func TestIsBackupFile_OnlyTheExactForm(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"opencloud-20261021-030000.tar.age", true},
		{"opencloud-key-0123456789abcdef.age", false},
		{".opencloud-20261021-030000.tar.age.part", false},
		{"opencloud-20261021-030000.tar.age.part", false},
		{"autre-20261021-030000.tar.age", false},
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
	earlier := BackupFileName(time.Date(2026, 10, 21, 3, 0, 0, 0, paris))
	later := BackupFileName(time.Date(2026, 10, 22, 3, 0, 0, 0, paris))
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
