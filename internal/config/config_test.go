package config

import (
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
