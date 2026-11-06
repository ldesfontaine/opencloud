package goldentest

import (
	"os"
	"path/filepath"
	"testing"
)

type recorder struct {
	testing.TB
	failed bool
}

func (r *recorder) Helper()               {}
func (r *recorder) Fatal(args ...any)     { r.failed = true }
func (r *recorder) Fatalf(string, ...any) { r.failed = true }

func TestCompare_SameContent_Passes(t *testing.T) {
	golden := filepath.Join(t.TempDir(), "a.golden.json")
	if err := os.WriteFile(golden, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	Compare(t, golden, "{}\n")
}

func TestCompare_DifferentContent_Fails(t *testing.T) {
	golden := filepath.Join(t.TempDir(), "a.golden.json")
	if err := os.WriteFile(golden, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &recorder{TB: t}
	Compare(r, golden, "{\"a\":1}\n")
	if !r.failed {
		t.Fatal("a different content must fail")
	}
}

func TestCompare_MissingReference_Fails(t *testing.T) {
	r := &recorder{TB: t}
	Compare(r, filepath.Join(t.TempDir(), "absent.golden.json"), "{}")
	if !r.failed {
		t.Fatal("a missing reference must fail")
	}
}
