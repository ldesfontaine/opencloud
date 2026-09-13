package hostinfo

import (
	"os"
	"path/filepath"
	"runtime"
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
