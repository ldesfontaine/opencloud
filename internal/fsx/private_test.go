package fsx

import (
	"errors"
	"io/fs"
	"os"
	"testing"
)

// Une clé se lit sur son descripteur : absente, trop ouverte ou trop
// longue, l'appelant le voit.
func TestReadPrivate_RefusesAFileOthersCanRead(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := ReadPrivate(root, "key", 16); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
	if err := root.WriteFile("key", []byte("0123456789abcdefXYZ"), 0o600); err != nil {
		t.Fatal(err)
	}
	content, err := ReadPrivate(root, "key", 16)
	if err != nil || string(content) != "0123456789abcdefX" {
		t.Fatalf("read %q %v", content, err)
	}
	if err := root.Chmod("key", 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPrivate(root, "key", 16); !errors.Is(err, ErrTooOpen) {
		t.Fatalf("readable by the group: %v", err)
	}
}
