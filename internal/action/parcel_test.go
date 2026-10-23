package action_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/action"
)

func newOutbox(t *testing.T) (*action.Outbox, *os.Root, *time.Time) {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	outbox, err := action.NewOutbox(root)
	if err != nil {
		t.Fatal(err)
	}
	now := benchNow
	outbox.SetClock(func() time.Time { return now })
	return outbox, root, &now
}

func writeParcelFile(t *testing.T, writer *action.ParcelWriter, name, content string) {
	t.Helper()
	file, err := writer.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(file, content); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

// Un colis ne se lit que par la machine à qui il est destiné, tant qu'il
// n'est pas échu ; sa taille et sa somme sont celles de ce qui a été écrit.
func TestOutbox_OpensOnlyForItsMachineAndUntilExpiry(t *testing.T) {
	outbox, root, now := newOutbox(t)
	writer, err := outbox.Create(machineID)
	if err != nil {
		t.Fatal(err)
	}
	writeParcelFile(t, writer, "opencloud-20261021-120000.tar.age", "contenu chiffré de test")
	id, files := writer.Seal()
	sum := sha256.Sum256([]byte("contenu chiffré de test"))
	if len(files) != 1 || files[0].Size != int64(len("contenu chiffré de test")) || files[0].SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("files %+v", files)
	}

	file, size, err := outbox.Open(machineID, id, files[0].Name)
	if err != nil {
		t.Fatal(err)
	}
	content, _ := io.ReadAll(file)
	_ = file.Close()
	if string(content) != "contenu chiffré de test" || size != files[0].Size {
		t.Fatalf("read %q (%d)", content, size)
	}
	info, err := root.Stat("outgoing/" + id + "/" + files[0].Name)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v %v", info, err)
	}

	for name, call := range map[string]func() error{
		"another machine": func() error { _, _, err := outbox.Open("autre", id, files[0].Name); return err },
		"another parcel":  func() error { _, _, err := outbox.Open(machineID, "inconnu", files[0].Name); return err },
		"another file":    func() error { _, _, err := outbox.Open(machineID, id, "autre.age"); return err },
		"a path":          func() error { _, _, err := outbox.Open(machineID, id, "../outgoing"); return err },
	} {
		if err := call(); !errors.Is(err, action.ErrParcelNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}

	*now = benchNow.Add(action.ParcelLifetime)
	if _, _, err := outbox.Open(machineID, id, files[0].Name); !errors.Is(err, action.ErrParcelNotFound) {
		t.Fatalf("an expired parcel is still served: %v", err)
	}
	outbox.Sweep()
	if _, err := root.Stat("outgoing/" + id); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the swept parcel is still on the disk: %v", err)
	}
}

// Un redémarrage ne garde aucun colis : ce que l'arrêt a laissé part, le
// dossier est recréé vide et fermé aux autres comptes.
func TestOutbox_NewClearsLeftovers(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.MkdirAll("outgoing/vieux", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFile("outgoing/vieux/restant.age", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := action.NewOutbox(root); err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(root.FS(), "outgoing")
	if err != nil || len(entries) != 0 {
		t.Fatalf("leftovers: %v %v", entries, err)
	}
	if info, err := root.Stat("outgoing"); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("outgoing mode %v %v", info, err)
	}
}

// Un colis jeté ne laisse rien ; un fichier emprunté à l'état est servi sans
// copie et n'est jamais effacé avec le colis ; un nom qui n'est pas simple
// est refusé.
func TestOutbox_DiscardBorrowAndNames(t *testing.T) {
	outbox, root, _ := newOutbox(t)
	if err := root.WriteFile("emprunt.bin", []byte("binaire de test"), 0o600); err != nil {
		t.Fatal(err)
	}
	writer, err := outbox.Create(machineID)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Borrow("agent.bin", "emprunt.bin"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"", "../x", "a/b", ".cache", "a b"} {
		if _, err := writer.Create(name); !errors.Is(err, action.ErrParcelName) {
			t.Errorf("%q: %v", name, err)
		}
	}
	id, files := writer.Seal()
	file, _, err := outbox.Open(machineID, id, "agent.bin")
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if len(files) != 1 {
		t.Fatalf("files %+v", files)
	}
	writer.Discard()
	if _, _, err := outbox.Open(machineID, id, "agent.bin"); !errors.Is(err, action.ErrParcelNotFound) {
		t.Fatalf("a discarded parcel is served: %v", err)
	}
	if _, err := root.Stat("emprunt.bin"); err != nil {
		t.Fatalf("the borrowed file went with the parcel: %v", err)
	}
	if _, err := root.Stat("outgoing/" + id); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the parcel dir is still there: %v", err)
	}
}

// L'instantané en clair d'un colis se relit puis s'efface : plus rien dans
// le dossier, et la liste de l'ordre ne le porte pas.
func TestParcelWriter_RemoveForgetsAStagedFile(t *testing.T) {
	outbox, root, _ := newOutbox(t)
	writer, err := outbox.Create(machineID)
	if err != nil {
		t.Fatal(err)
	}
	writeParcelFile(t, writer, "snapshot.db", "base de test")
	writeParcelFile(t, writer, "archive.age", "archive de test")
	file, size, err := writer.Open("snapshot.db")
	if err != nil || size != int64(len("base de test")) {
		t.Fatalf("open staged: %d %v", size, err)
	}
	_ = file.Close()
	if err := writer.Remove("snapshot.db"); err != nil {
		t.Fatal(err)
	}
	id, files := writer.Seal()
	if len(files) != 1 || files[0].Name != "archive.age" {
		t.Fatalf("files %+v", files)
	}
	if _, err := root.Stat("outgoing/" + id + "/snapshot.db"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the clear copy is still there: %v", err)
	}
}
