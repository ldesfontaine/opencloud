package fsx

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func openTestRoot(t *testing.T) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatalf("ouvrir le root : %v", err)
	}
	t.Cleanup(func() { root.Close() })
	return root
}

func TestWriteFile_NewFile_HasContentAndMode(t *testing.T) {
	root := openTestRoot(t)

	if err := WriteFile(root, "token", []byte("secret"), 0o600); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	content, err := root.ReadFile("token")
	if err != nil {
		t.Fatalf("relire : %v", err)
	}
	if string(content) != "secret" {
		t.Fatalf("contenu = %q, attendu %q", content, "secret")
	}
	info, err := root.Stat("token")
	if err != nil {
		t.Fatalf("stat : %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, attendu 600", info.Mode().Perm())
	}
}

func TestWriteFile_ExistingFile_IsReplacedWithoutLeftover(t *testing.T) {
	root := openTestRoot(t)
	if err := root.WriteFile("config", []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := WriteFile(root, "config", []byte("new"), 0o644); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	content, _ := root.ReadFile("config")
	if string(content) != "new" {
		t.Fatalf("contenu = %q, attendu %q", content, "new")
	}
	if _, err := root.Stat("config" + temporarySuffix); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("le temporaire doit avoir disparu, stat = %v", err)
	}
}

func TestWriteFile_StaleTemporary_IsRemovedFirst(t *testing.T) {
	root := openTestRoot(t)
	if err := root.WriteFile("config"+temporarySuffix, []byte("half"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := WriteFile(root, "config", []byte("whole"), 0o644); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	content, _ := root.ReadFile("config")
	if string(content) != "whole" {
		t.Fatalf("contenu = %q, attendu %q", content, "whole")
	}
}

func TestWriteFile_InSubdirectory_SyncsThatDirectory(t *testing.T) {
	root := openTestRoot(t)
	if err := root.Mkdir("keys", 0o700); err != nil {
		t.Fatal(err)
	}

	if err := WriteFile(root, "keys/machine", []byte("key"), 0o600); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	if _, err := root.Stat("keys/machine"); err != nil {
		t.Fatalf("le fichier doit exister : %v", err)
	}
}

func TestWriteFile_SymlinkTarget_IsNotFollowed(t *testing.T) {
	root := openTestRoot(t)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("untouched"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := root.Symlink(outside, "config"+temporarySuffix); err != nil {
		t.Fatal(err)
	}

	// Le lien est retiré comme un temporaire périmé ; la cible hors root reste intacte.
	if err := WriteFile(root, "config", []byte("new"), 0o644); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}

	content, _ := os.ReadFile(outside)
	if string(content) != "untouched" {
		t.Fatalf("la cible du lien a été écrite : %q", content)
	}
}

func TestSyncDirectory_Root_Works(t *testing.T) {
	root := openTestRoot(t)

	if err := SyncDirectory(root, "."); err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
}
