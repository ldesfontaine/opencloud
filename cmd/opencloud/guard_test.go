package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGuardCommand_WithoutTheAgentLineIsARefusal(t *testing.T) {
	err := runGuard([]string{"-state", t.TempDir()})
	var refused *refusal
	if !errors.As(err, &refused) || refused.message == "" {
		t.Fatalf("got %v", err)
	}
}

func TestGuardCommand_NeedsTheBinFolderOfTheState(t *testing.T) {
	if err := runGuard([]string{"-state", t.TempDir(), "agent"}); err == nil {
		t.Fatal("a state without bin/ started")
	}
}

// La commande lance bin/current avec le reste de la ligne et rend son code à
// systemd : 3 veut dire « relance-moi », rien d'autre n'est inventé.
func TestGuardCommand_RunsCurrentAndReturnsItsExitCode(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	state := t.TempDir()
	bin := filepath.Join(state, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil { // #nosec G301 -- un dossier de test
		t.Fatal(err)
	}
	witness := filepath.Join(t.TempDir(), "args")
	body := "#!" + shell + "\nprintf '%s' \"$*\" > " + witness + "\nexit 3\n"
	if err := os.WriteFile(filepath.Join(bin, "opencloud-v0.0.1"), []byte(body), 0o700); err != nil { // #nosec G306 -- un binaire factice de test
		t.Fatal(err)
	}
	if err := os.Symlink("opencloud-v0.0.1", filepath.Join(bin, "current")); err != nil {
		t.Fatal(err)
	}
	err = runGuard([]string{"-state", state, "agent", "-state", state, "-ca-file", "/etc/ca.pem"})
	var status *exitStatus
	if !errors.As(err, &status) || status.code != 3 {
		t.Fatalf("got %v", err)
	}
	got, readErr := os.ReadFile(witness) // #nosec G304 -- le témoin du test.
	if readErr != nil || string(got) != "agent -state "+state+" -ca-file /etc/ca.pem" {
		t.Fatalf("the agent line was %q (%v)", got, readErr)
	}
}
