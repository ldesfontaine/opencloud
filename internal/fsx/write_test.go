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
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil || len(entries) != 1 {
		t.Fatalf("temp file left behind: %v, %v", entries, err)
	}
	info, err := root.Stat("settings.toml")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode().Perm())
	}
}

// Une coupure entre la création du temporaire et le rename en laisse un,
// avec une copie de ce qui s'écrivait : l'écriture suivante le supprime,
// sans toucher à ce qui n'est pas un des siens.
func TestWriteAtomic_RemovesTempsLeftByACut(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	leftover := "secret.key.tmp-0123456789abcdef"
	kept := []string{"secret.key.tmp", "secret.key.tmp-notours", "other.tmp-0123456789abcdef"}
	for _, name := range append([]string{leftover}, kept...) {
		if err := root.WriteFile(name, []byte("old key"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := WriteAtomic(root, "secret.key", []byte("new key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Stat(leftover); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("leftover temp still there: %v", err)
	}
	for _, name := range kept {
		if _, err := root.Stat(name); err != nil {
			t.Fatalf("%s was removed: %v", name, err)
		}
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

func TestWriteAtomic_NeverReusesAPlantedTemp(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	// Un fichier posé d'avance au nom fixe « <chemin>.tmp » par un autre compte.
	if err := root.WriteFile("secret.tmp", []byte("planted"), 0o644); err != nil {
		t.Fatal(err)
	}
	plantedBefore, err := root.Stat("secret.tmp")
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(root, "secret", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	planted, err := root.Stat("secret.tmp")
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(plantedBefore, planted) || planted.Mode().Perm() != 0o644 {
		t.Fatal("the planted file was touched")
	}
	content, err := root.ReadFile("secret.tmp")
	if err != nil || string(content) != "planted" {
		t.Fatalf("planted content %q, %v", content, err)
	}

	final, err := root.Stat("secret")
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(plantedBefore, final) {
		t.Fatal("the final file is the planted inode")
	}
	if final.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want 0600", final.Mode().Perm())
	}
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil || len(entries) != 2 {
		t.Fatalf("unexpected leftovers: %v, %v", entries, err)
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
