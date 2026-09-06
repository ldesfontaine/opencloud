package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse_OnlyStateDir_UsesDefaultListen(t *testing.T) {
	cfg, warnings, err := Parse([]byte("state_dir = \"state\"\n"))
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if cfg.Listen != defaults().Listen || cfg.StateDir != "state" {
		t.Fatalf("config = %+v", cfg)
	}
	if len(warnings) != 0 {
		t.Fatalf("avertissements inattendus : %v", warnings)
	}
}

func TestParse_WithoutStateDir_IsRefusedNamingTheKey(t *testing.T) {
	_, _, err := Parse(nil)
	if err == nil || !strings.Contains(err.Error(), "state_dir") {
		t.Fatalf("state_dir est obligatoire, reçu %v", err)
	}
}

func TestParse_ValidFile_OverridesDefaults(t *testing.T) {
	content := []byte("listen = \"0.0.0.0:9000\"\nstate_dir = \"state\"\n")

	cfg, _, err := Parse(content)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if cfg.Listen != "0.0.0.0:9000" {
		t.Fatalf("listen = %q", cfg.Listen)
	}
}

func TestParse_AllowDefaultPassword_IsFalseUnlessSaid(t *testing.T) {
	if defaults().AllowDefaultPassword {
		t.Fatal("en production, le mot de passe par défaut doit changer")
	}

	cfg, _, err := Parse([]byte("state_dir = \"state\"\nallow_default_password = true\n"))
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if !cfg.AllowDefaultPassword {
		t.Fatal("la clé doit être lue")
	}
}

func TestParse_MinPasswordLength_DefaultsToTwelveAndCanBeLifted(t *testing.T) {
	cfg, _, err := Parse([]byte("state_dir = \"state\"\n"))
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if cfg.MinPasswordLength != DefaultMinPasswordLength {
		t.Fatalf("min_password_length = %d, attendu %d", cfg.MinPasswordLength, DefaultMinPasswordLength)
	}

	cfg, _, err = Parse([]byte("state_dir = \"state\"\nmin_password_length = 0\n"))
	if err != nil {
		t.Fatalf("lever la règle doit être permis : %v", err)
	}
	if cfg.MinPasswordLength != 0 {
		t.Fatalf("min_password_length = %d, attendu 0", cfg.MinPasswordLength)
	}

	for _, content := range []string{"min_password_length = -1\n", "min_password_length = 65\n"} {
		_, _, err := Parse([]byte("state_dir = \"state\"\n" + content))
		if err == nil || !strings.Contains(err.Error(), "min_password_length") {
			t.Fatalf("%q doit être refusé en nommant la clé, reçu %v", content, err)
		}
	}
}

func TestParse_RetiredGitHubToken_IsAWarningThatGivesTheGesture(t *testing.T) {
	_, warnings, err := Parse([]byte("state_dir = \"state\"\ngithub_token = \"github_pat_abc\"\n"))
	if err != nil {
		t.Fatalf("une clé retirée ne doit pas empêcher de démarrer : %v", err)
	}
	if len(warnings) != 1 || warnings[0].Key != "github_token" {
		t.Fatalf("avertissements = %v, attendu github_token nommé", warnings)
	}

	message := warnings[0].String()
	for _, expected := range []string{"github_token", "/etc/opencloud/github-token", "0600 root:root"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("le message doit dire %q : %q", expected, message)
		}
	}
	if strings.Contains(message, "github_pat_abc") {
		t.Fatalf("l'avertissement recopie la valeur : %q", message)
	}
}

func TestParse_UnknownKey_IsAWarningThatNamesIt(t *testing.T) {
	content := []byte("state_dir = \"state\"\nlisten = \"127.0.0.1:1\"\nlisten_addr = \"x\"\n[web]\ncolour = \"blue\"\n")

	cfg, warnings, err := Parse(content)
	if err != nil {
		t.Fatalf("une clé inconnue ne doit pas être une erreur : %v", err)
	}
	if cfg.Listen != "127.0.0.1:1" {
		t.Fatalf("les clés connues doivent être décodées, listen = %q", cfg.Listen)
	}
	if len(warnings) != 2 {
		t.Fatalf("avertissements = %v, attendu 2", warnings)
	}
	// Une table inconnue est nommée par la table, pas par chacune de ses clés.
	if warnings[0].Key != "listen_addr" || warnings[1].Key != "web" {
		t.Fatalf("clés nommées = %v", warnings)
	}
	if !strings.Contains(warnings[0].String(), "listen_addr") {
		t.Fatalf("le message doit nommer la clé : %q", warnings[0].String())
	}
}

func TestParse_InvalidListen_IsARefusalThatNamesTheKey(t *testing.T) {
	cases := map[string]string{
		"sans port":       "state_dir = \"state\"\nlisten = \"127.0.0.1\"\n",
		"port hors borne": "state_dir = \"state\"\nlisten = \"127.0.0.1:99999\"\n",
		"hôte fantaisie":  "state_dir = \"state\"\nlisten = \"pas un hôte:80\"\n",
		"mauvais type":    "state_dir = \"state\"\nlisten = 8080\n",
	}

	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := Parse([]byte(content))
			if err == nil {
				t.Fatal("une valeur invalide doit être refusée")
			}
			if !strings.Contains(err.Error(), "listen") {
				t.Fatalf("le refus doit nommer la clé : %v", err)
			}
		})
	}
}

func TestParse_MalformedToml_IsRefusedWithPosition(t *testing.T) {
	_, _, err := Parse([]byte("listen = \n"))
	if err == nil || !strings.Contains(err.Error(), "ligne") {
		t.Fatalf("attendu un refus avec la position, reçu %v", err)
	}
}

func TestLoad_MissingFile_IsAnError(t *testing.T) {
	_, _, err := Load(filepath.Join(t.TempDir(), "absent.toml"))
	if err == nil {
		t.Fatal("un fichier absent doit être une erreur")
	}
}

func TestLoad_RelativeStateDir_ResolvesAgainstTheConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("listen = \"[::1]:8081\"\nstate_dir = \"state\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, _, err := Load(path)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if cfg.Listen != "[::1]:8081" {
		t.Fatalf("listen = %q", cfg.Listen)
	}
	if want := filepath.Join(dir, "state"); cfg.StateDir != want {
		t.Fatalf("state_dir = %q, attendu %q", cfg.StateDir, want)
	}
}

func TestLoad_AbsoluteStateDir_IsKept(t *testing.T) {
	stateDir := t.TempDir()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("state_dir = \""+stateDir+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, _, err := Load(path)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	if cfg.StateDir != stateDir {
		t.Fatalf("state_dir = %q, attendu %q", cfg.StateDir, stateDir)
	}
}
