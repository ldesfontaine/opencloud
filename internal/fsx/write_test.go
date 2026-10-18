package fsx

import (
	"errors"
	"io/fs"
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

func TestWriteAtomic_FixesTheModeOfALeftoverTemp(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	// Un temporaire resté d'une écriture interrompue, avec un mode large.
	if err := root.WriteFile("secret.tmp", []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := root.Chmod("secret.tmp", 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(root, "secret", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := root.Stat("secret")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", info.Mode().Perm())
	}
}

func TestCreateExclusive_NeverReplacesAnExistingFile(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := CreateExclusive(root, "key", []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CreateExclusive(root, "key", []byte("second"), 0o600); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("got %v, want fs.ErrExist", err)
	}
	got, err := root.ReadFile("key")
	if err != nil || string(got) != "first" {
		t.Fatalf("content %q, %v", got, err)
	}
	info, err := root.Stat("key")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", info.Mode().Perm())
	}
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil || len(entries) != 1 {
		t.Fatalf("a temporary was left behind: %v, %v", entries, err)
	}
}
