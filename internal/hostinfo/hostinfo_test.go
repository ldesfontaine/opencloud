package hostinfo

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReadOSName_ComposesNameAndVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "os-release")
	content := "PRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\nNAME=\"Debian GNU/Linux\"\nVERSION_ID=\"12\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readOSName(path); got != "Debian GNU/Linux 12" {
		t.Fatalf("got %q", got)
	}
}

func TestReadOSName_MissingFile_FallsBackToGoos(t *testing.T) {
	if got := readOSName(filepath.Join(t.TempDir(), "absent")); got != runtime.GOOS {
		t.Fatalf("got %q", got)
	}
}

func TestCollect_FillsArchAtLeast(t *testing.T) {
	if got := Collect(); got.Arch != runtime.GOARCH {
		t.Fatalf("got %+v", got)
	}
}

func writeIdentifier(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadHostFingerprint_HashesTheFirstIdentifierPresent(t *testing.T) {
	dir := t.TempDir()
	dbus := writeIdentifier(t, dir, "dbus-machine-id", "4c1f0e6f9b2d4a3e8f7a6b5c4d3e2f10")
	boot := writeIdentifier(t, dir, "boot_id", "9a6c1d2e-0b7f-4e3a-8c5d-1f2e3a4b5c6d")
	fingerprint := readHostFingerprint([]string{filepath.Join(dir, "absent"), dbus}, boot)
	if len(fingerprint) != 64 || strings.Contains(fingerprint, "4c1f0e6f") {
		t.Fatalf("fingerprint %q", fingerprint)
	}
	if again := readHostFingerprint([]string{dbus}, boot); again != fingerprint {
		t.Fatalf("the same identifiers give the same fingerprint: %q", again)
	}
}

// Deux clones d'une même image gardent le machine-id du modèle : seul le
// démarrage les distingue.
func TestReadHostFingerprint_ClonesWithTheSameMachineID_Differ(t *testing.T) {
	dir := t.TempDir()
	machineID := writeIdentifier(t, dir, "machine-id", "dc14e41f6a4344cfa449f5b74479a1bb")
	bootA := writeIdentifier(t, dir, "boot-a", "11111111-1111-4111-8111-111111111111")
	bootB := writeIdentifier(t, dir, "boot-b", "22222222-2222-4222-8222-222222222222")
	if readHostFingerprint([]string{machineID}, bootA) == readHostFingerprint([]string{machineID}, bootB) {
		t.Fatal("two clones booted separately share a fingerprint")
	}
}

func TestReadHostFingerprint_NoIdentifier_GivesNothing(t *testing.T) {
	dir := t.TempDir()
	machineID := writeIdentifier(t, dir, "machine-id", "dc14e41f6a4344cfa449f5b74479a1bb")
	boot := writeIdentifier(t, dir, "boot_id", "11111111-1111-4111-8111-111111111111")
	absent := filepath.Join(dir, "absent")
	if got := readHostFingerprint([]string{absent}, boot); got != "" {
		t.Fatalf("no machine-id: got %q", got)
	}
	if got := readHostFingerprint([]string{machineID}, absent); got != "" {
		t.Fatalf("no boot_id: got %q", got)
	}
}
