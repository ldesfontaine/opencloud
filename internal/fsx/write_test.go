package fsx

import (
	"os"
	"testing"
)

func TestWriteAtomic_ReplacesTheFileAndLeavesNoTemp(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	if err := WriteAtomic(root, "settings.toml", []byte("a = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(root, "settings.toml", []byte("a = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := root.ReadFile("settings.toml")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "a = 2\n" {
		t.Fatalf("got %q", got)
	}
	if _, err := root.Stat("settings.toml.tmp"); !os.IsNotExist(err) {
		t.Fatalf("temp file left behind: %v", err)
	}
	info, err := root.Stat("settings.toml")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode().Perm())
	}
}

func TestWriteAtomic_RefusesToLeaveTheRoot(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := WriteAtomic(root, "../escape.toml", []byte("x"), 0o600); err == nil {
		t.Fatal("wrote outside the root")
	}
}
