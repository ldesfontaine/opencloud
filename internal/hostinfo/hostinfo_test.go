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

func TestReadHostFingerprint_HashesTheFirstIdentifierPresent(t *testing.T) {
	dir := t.TempDir()
	dbus := filepath.Join(dir, "dbus-machine-id")
	if err := os.WriteFile(dbus, []byte("4c1f0e6f9b2d4a3e8f7a6b5c4d3e2f10\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fingerprint := readHostFingerprint([]string{filepath.Join(dir, "absent"), dbus})
	if len(fingerprint) != 64 || strings.Contains(fingerprint, "4c1f0e6f") {
		t.Fatalf("fingerprint %q", fingerprint)
	}
	if again := readHostFingerprint([]string{dbus}); again != fingerprint {
		t.Fatalf("the same identifier gives the same fingerprint: %q", again)
	}
}

func TestReadHostFingerprint_NoIdentifier_GivesNothing(t *testing.T) {
	if got := readHostFingerprint([]string{filepath.Join(t.TempDir(), "absent")}); got != "" {
		t.Fatalf("got %q", got)
	}
}
