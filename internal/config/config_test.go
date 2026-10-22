package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse_EmptyFile_UsesDefaults(t *testing.T) {
	cfg, warnings, err := Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != Default().Listen || cfg.StateDir != Default().StateDir || cfg.PublicURL != "" || len(cfg.TrustedProxies) != 0 {
		t.Fatalf("got %+v, want defaults", cfg)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings %v", warnings)
	}
}

func TestParse_KnownKeys_AreApplied(t *testing.T) {
	cfg, _, err := Parse([]byte("listen = \"0.0.0.0:9090\"\nstate_dir = \"/tmp/oc\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "0.0.0.0:9090" || cfg.StateDir != "/tmp/oc" {
		t.Fatalf("got %+v", cfg)
	}
}

func TestParse_UnknownKey_WarnsAndKeepsTheRest(t *testing.T) {
	cfg, warnings, err := Parse([]byte("listen = \"127.0.0.1:1\"\ncolour = \"blue\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:1" {
		t.Fatalf("known key lost: %+v", cfg)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "colour") {
		t.Fatalf("want one warning naming colour, got %v", warnings)
	}
}

func TestParse_BadListen_IsRejected(t *testing.T) {
	if _, _, err := Parse([]byte("listen = \"not an address\"\n")); err == nil {
		t.Fatal("want a validation error")
	}
}

func TestParse_BadToml_IsRejected(t *testing.T) {
	if _, _, err := Parse([]byte("listen = \n")); err == nil {
		t.Fatal("want a syntax error")
	}
}

func TestLoad_ReadsTheFileAtThePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("listen = \"127.0.0.1:7\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:7" {
		t.Fatalf("got %+v", cfg)
	}
	if _, _, err := Load(filepath.Join(t.TempDir(), "absent.toml")); err == nil {
		t.Fatal("want an error for a missing file")
	}
}

func TestParse_PublicURLAndProxies_AreValidated(t *testing.T) {
	cfg, _, err := Parse([]byte("public_url = \"https://oc.example.fr\"\ntrusted_proxies = [\"127.0.0.1/32\", \"10.0.0.0/8\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PublicURL != "https://oc.example.fr" || len(cfg.TrustedPrefixes()) != 2 {
		t.Fatalf("got %+v", cfg)
	}
	if _, _, err := Parse([]byte("public_url = \"oc.example.fr\"\n")); err == nil {
		t.Error("public_url without scheme accepted")
	}
	if _, _, err := Parse([]byte("trusted_proxies = [\"not-a-cidr\"]\n")); err == nil {
		t.Error("bad cidr accepted")
	}
}

// mcp_url est l'adresse de mcp-maison : vide, openCloud ne le cherche pas ;
// sinon une adresse http(s), sans quoi le démarrage la refuse.
func TestParse_MCPURL_IsEmptyByDefaultAndMustBeAnHTTPAddress(t *testing.T) {
	cfg, _, err := Parse([]byte(""))
	if err != nil || cfg.MCPURL != "" {
		t.Fatalf("default: %q %v", cfg.MCPURL, err)
	}
	cfg, _, err = Parse([]byte("mcp_url = \"http://127.0.0.1:3000\"\n"))
	if err != nil || cfg.MCPURL != "http://127.0.0.1:3000" {
		t.Fatalf("set: %q %v", cfg.MCPURL, err)
	}
	for _, bad := range []string{"127.0.0.1:3000", "ftp://127.0.0.1:3000", "mcp-maison"} {
		if _, _, err := Parse([]byte("mcp_url = \"" + bad + "\"\n")); err == nil {
			t.Errorf("mcp_url %q accepted", bad)
		}
	}
}

// Un mandataire déclaré par un sous-réseau est cru pour chacun de ses pairs :
// le démarrage le dit, sans refuser la configuration.
func TestParse_WideTrustedProxy_Warns(t *testing.T) {
	_, warnings, err := Parse([]byte("trusted_proxies = [\"127.0.0.1/32\", \"::1/128\", \"172.18.0.0/16\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "172.18.0.0/16") {
		t.Fatalf("want one warning naming 172.18.0.0/16, got %v", warnings)
	}
}

func TestParse_ForwardSecretFile_IsAPathNotTheSecret(t *testing.T) {
	cfg, warnings, err := Parse([]byte("forward_secret_file = \"/etc/opencloud/forward.secret\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ForwardSecretFile != "/etc/opencloud/forward.secret" || len(warnings) != 0 {
		t.Fatalf("got %+v, warnings %v", cfg, warnings)
	}
	if Default().ForwardSecretFile != "" {
		t.Fatal("no secret by default")
	}
}

func TestParse_ForwardUsers_AbsentOrEmptyMeansEveryone(t *testing.T) {
	for _, content := range []string{"", "forward_users = []\n"} {
		cfg, _, err := Parse([]byte(content))
		if err != nil {
			t.Fatalf("%q: %v", content, err)
		}
		if len(cfg.ForwardUsers) != 0 {
			t.Fatalf("%q: got %v", content, cfg.ForwardUsers)
		}
	}
}

func TestParse_ForwardUsers_KeepsTheEntriesAsWritten(t *testing.T) {
	// Deux entrées qui ne diffèrent que par la casse sont deux identités.
	cfg, _, err := Parse([]byte("forward_users = [\"lucas\", \"Lucas\", \"léa@example.org\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cfg.ForwardUsers, "|"); got != "lucas|Lucas|léa@example.org" {
		t.Fatalf("got %q", got)
	}
}

func TestParse_ForwardUsers_AFalseListStopsTheStart(t *testing.T) {
	cases := []struct{ name, entries string }{
		{"empty entry", `""`},
		{"empty entry next to a good one", `"lucas", ""`},
		{"leading space", `" lucas"`},
		{"trailing space", `"lucas "`},
		{"only spaces", `"   "`},
		{"trailing tab", `"lucas\t"`},
		{"no-break space at the edge", `"lucas "`},
		{"newline inside", `"lu\ncas"`},
		{"NUL inside", `"lu\u0000cas"`},
		{"escape inside", `"lu\u001bcas"`},
		{"twice", `"lucas", "lucas"`},
		{"twice, apart", `"lucas", "lea", "lucas"`},
		{"too long", `"` + strings.Repeat("a", 65) + `"`},
		{"too long in characters, not bytes", `"` + strings.Repeat("é", 65) + `"`},
	}
	for _, tc := range cases {
		if _, _, err := Parse([]byte("forward_users = [" + tc.entries + "]\n")); err == nil {
			t.Errorf("%s: the list must be refused", tc.name)
		}
	}
}

func TestParse_ForwardUsers_TheLimitsAreInclusive(t *testing.T) {
	longest := strings.Repeat("é", 64)
	if _, _, err := Parse([]byte("forward_users = [\"" + longest + "\"]\n")); err != nil {
		t.Fatalf("64 characters: %v", err)
	}
	entries := func(count int) string {
		quoted := make([]string, count)
		for index := range quoted {
			quoted[index] = fmt.Sprintf("%q", fmt.Sprintf("user-%02d", index))
		}
		return "forward_users = [" + strings.Join(quoted, ", ") + "]\n"
	}
	if _, _, err := Parse([]byte(entries(64))); err != nil {
		t.Fatalf("64 entries: %v", err)
	}
	if _, _, err := Parse([]byte(entries(65))); err == nil {
		t.Fatal("65 entries must be refused")
	}
}

// Le message nomme l'entrée fautive, sans laisser un caractère de contrôle
// y passer tel quel.
func TestParse_ForwardUsers_TheErrorNamesTheEntryWithoutControlCharacters(t *testing.T) {
	_, _, err := Parse([]byte("forward_users = [\"lu\\ncas\"]\n"))
	if err == nil {
		t.Fatal("must be refused")
	}
	if strings.ContainsRune(err.Error(), '\n') || !strings.Contains(err.Error(), "forward_users") {
		t.Fatalf("got %q", err)
	}
}
