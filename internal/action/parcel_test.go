package action_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"strings"
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

const (
	producerID    = "99999999-8888-4777-8666-555555555555"
	uploadedName  = "pve-vm-5012-20261021-030000.tar.age"
	uploadedBytes = "archive chiffrée de test"
)

func sumOf(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func receive(outbox *action.Outbox, uploader, parcel, name, content string) error {
	return outbox.Receive(uploader, parcel, name, strings.NewReader(content), int64(len(content)), sumOf(content))
}

// Le colis téléversé s'ouvre pour la destination, pas pour le producteur : le
// serveur garde l'archive le temps que la destination la prenne.
func TestOutbox_ReceiveFillsAParcelTheDestinationReads(t *testing.T) {
	outbox, root, _ := newOutbox(t)
	upload, err := outbox.CreateForUpload(machineID, producerID)
	if err != nil {
		t.Fatal(err)
	}
	if files := upload.Files(); len(files) != 0 {
		t.Fatalf("an empty parcel lists %+v", files)
	}
	if err := receive(outbox, producerID, upload.ID(), uploadedName, uploadedBytes); err != nil {
		t.Fatal(err)
	}
	files := upload.Files()
	if len(files) != 1 || files[0].Name != uploadedName || files[0].Size != int64(len(uploadedBytes)) || files[0].SHA256 != sumOf(uploadedBytes) {
		t.Fatalf("files %+v", files)
	}
	file, size, err := outbox.Open(machineID, upload.ID(), uploadedName)
	if err != nil {
		t.Fatal(err)
	}
	content, _ := io.ReadAll(file)
	_ = file.Close()
	if string(content) != uploadedBytes || size != files[0].Size {
		t.Fatalf("read %q (%d)", content, size)
	}
	if _, _, err := outbox.Open(producerID, upload.ID(), uploadedName); !errors.Is(err, action.ErrParcelNotFound) {
		t.Fatalf("the producer reads the parcel back: %v", err)
	}
	info, err := root.Stat("outgoing/" + upload.ID() + "/" + uploadedName)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v %v", info, err)
	}
	entries, _ := fs.ReadDir(root.FS(), "outgoing/"+upload.ID())
	if len(entries) != 1 {
		t.Fatalf("the parcel dir holds %d entries, want only the archive", len(entries))
	}
	upload.Discard()
	if _, err := root.Stat("outgoing/" + upload.ID()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the discarded parcel is still on the disk: %v", err)
	}
	if _, _, err := outbox.Open(machineID, upload.ID(), uploadedName); !errors.Is(err, action.ErrParcelNotFound) {
		t.Fatalf("a discarded parcel is served: %v", err)
	}
}

// Seul l'agent désigné écrit, et seulement dans un colis ouvert pour être
// rempli, tant qu'il n'est pas échu : tout autre cas se dit comme un colis
// inconnu.
func TestOutbox_ReceiveOnlyFromTheDesignatedUploader(t *testing.T) {
	outbox, _, now := newOutbox(t)
	upload, err := outbox.CreateForUpload(machineID, producerID)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := outbox.Create(machineID)
	if err != nil {
		t.Fatal(err)
	}
	sealedID, _ := sealed.Seal()
	for name, call := range map[string]func() error{
		"another machine":            func() error { return receive(outbox, "autre", upload.ID(), uploadedName, uploadedBytes) },
		"the destination itself":     func() error { return receive(outbox, machineID, upload.ID(), uploadedName, uploadedBytes) },
		"an unknown parcel":          func() error { return receive(outbox, producerID, "inconnu", uploadedName, uploadedBytes) },
		"a parcel the server sealed": func() error { return receive(outbox, producerID, sealedID, uploadedName, uploadedBytes) },
		"no uploader":                func() error { return receive(outbox, "", sealedID, uploadedName, uploadedBytes) },
	} {
		if err := call(); !errors.Is(err, action.ErrParcelNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if files := upload.Files(); len(files) != 0 {
		t.Fatalf("a refused upload left %+v", files)
	}
	*now = benchNow.Add(action.ParcelLifetime)
	if err := receive(outbox, producerID, upload.ID(), uploadedName, uploadedBytes); !errors.Is(err, action.ErrParcelNotFound) {
		t.Fatalf("an expired parcel still receives: %v", err)
	}
}

// Ce que le serveur écrit est ce que l'agent a annoncé : une somme fausse, un
// fichier plus court ou plus long ne laissent rien, et le colis peut être
// rempli ensuite.
func TestOutbox_ReceiveKeepsNothingOfAWrongUpload(t *testing.T) {
	outbox, root, _ := newOutbox(t)
	upload, err := outbox.CreateForUpload(machineID, producerID)
	if err != nil {
		t.Fatal(err)
	}
	size := int64(len(uploadedBytes))
	for name, call := range map[string]func() error{
		"another sum": func() error {
			return outbox.Receive(producerID, upload.ID(), uploadedName, strings.NewReader(uploadedBytes), size, sumOf("autre"))
		},
		"a shorter file": func() error {
			return outbox.Receive(producerID, upload.ID(), uploadedName, strings.NewReader(uploadedBytes[:5]), size, sumOf(uploadedBytes))
		},
		"a longer file": func() error {
			return outbox.Receive(producerID, upload.ID(), uploadedName, strings.NewReader(uploadedBytes+"en trop"), size, sumOf(uploadedBytes))
		},
	} {
		if err := call(); !errors.Is(err, action.ErrUploadDigest) {
			t.Errorf("%s: %v", name, err)
		}
		if entries, _ := fs.ReadDir(root.FS(), "outgoing/"+upload.ID()); len(entries) != 0 {
			t.Errorf("%s left %d files", name, len(entries))
		}
	}
	if err := receive(outbox, producerID, upload.ID(), uploadedName, uploadedBytes); err != nil {
		t.Fatalf("the parcel did not take a good upload after the bad ones: %v", err)
	}
}

// Le nom est celui d'une archive produite, rien d'autre : ni un chemin, ni une
// clé déposée, ni un .part, ni un nom en majuscules.
func TestOutbox_ReceiveOnlyTakesProducedArchiveNames(t *testing.T) {
	outbox, root, _ := newOutbox(t)
	upload, err := outbox.CreateForUpload(machineID, producerID)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"", "autre.txt", "../pve-vm-5012-20261021-030000.tar.age", "a/b-20261021-030000.tar.age",
		"opencloud-key-0123456789abcdef.age", uploadedName + ".part", "PVE-20261021-030000.tar.age",
		"pve-20261021-030000.tar", "-pve-20261021-030000.tar.age", uploadedName + "\n",
	} {
		if err := receive(outbox, producerID, upload.ID(), name, uploadedBytes); !errors.Is(err, action.ErrUploadName) {
			t.Errorf("%q: %v", name, err)
		}
	}
	if entries, _ := fs.ReadDir(root.FS(), "outgoing/"+upload.ID()); len(entries) != 0 {
		t.Fatalf("a refused name left %d files", len(entries))
	}
}

// La taille maximale se juge sur l'annonce, avant de lire un octet ; le
// disque garde sa réserve (MinFreeAfterUpload) une fois le fichier écrit.
func TestOutbox_ReceiveRefusesAFileTooLargeOrADiskTooFull(t *testing.T) {
	outbox, _, _ := newOutbox(t)
	upload, err := outbox.CreateForUpload(machineID, producerID)
	if err != nil {
		t.Fatal(err)
	}
	untouched := readerFunc(func([]byte) (int, error) {
		t.Error("a refused upload was read")
		return 0, io.EOF
	})
	for _, size := range []int64{action.MaxUploadBytes + 1, -1} {
		if err := outbox.Receive(producerID, upload.ID(), uploadedName, untouched, size, sumOf("")); !errors.Is(err, action.ErrUploadTooLarge) {
			t.Errorf("size %d: %v", size, err)
		}
	}
	size := int64(len(uploadedBytes))
	free := uint64(size) + action.MinFreeAfterUpload - 1
	outbox.SetFreeBytes(func(string) (uint64, error) { return free, nil })
	if err := outbox.Receive(producerID, upload.ID(), uploadedName, untouched, size, sumOf(uploadedBytes)); !errors.Is(err, action.ErrUploadDiskFull) {
		t.Fatalf("one byte short of the reserve: %v", err)
	}
	free++
	if err := receive(outbox, producerID, upload.ID(), uploadedName, uploadedBytes); err != nil {
		t.Fatalf("exactly the reserve: %v", err)
	}
	// Une mesure impossible ne bloque pas : l'écriture dira si elle échoue.
	outbox.SetFreeBytes(func(string) (uint64, error) { return 0, errors.New("statfs") })
	if err := receive(outbox, producerID, upload.ID(), "pve-vm-5013-20261021-030000.tar.age", uploadedBytes); err != nil {
		t.Fatalf("an unreadable free space blocks: %v", err)
	}
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

// Un nom déjà écrit, un fichier déjà en vol et un colis plein se refusent :
// un seul fichier arrive à la fois dans un colis.
func TestOutbox_ReceiveWritesOneFileAtATimeAndNeverTwice(t *testing.T) {
	outbox, _, _ := newOutbox(t)
	upload, err := outbox.CreateForUpload(machineID, producerID)
	if err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- outbox.Receive(producerID, upload.ID(), uploadedName, reader, int64(len(uploadedBytes)), sumOf(uploadedBytes))
	}()
	// Le premier octet n'est lu qu'une fois le colis réservé.
	if _, err := writer.Write([]byte(uploadedBytes[:1])); err != nil {
		t.Fatal(err)
	}
	other := "pve-vm-5013-20261021-030000.tar.age"
	if err := receive(outbox, producerID, upload.ID(), other, uploadedBytes); !errors.Is(err, action.ErrUploadConflict) {
		t.Fatalf("a second file while one is in flight: %v", err)
	}
	if _, err := writer.Write([]byte(uploadedBytes[1:])); err != nil {
		t.Fatal(err)
	}
	// La fin du corps est la fin du fichier.
	_ = writer.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := receive(outbox, producerID, upload.ID(), uploadedName, uploadedBytes); !errors.Is(err, action.ErrUploadConflict) {
		t.Fatalf("the same name twice: %v", err)
	}
	if err := receive(outbox, producerID, upload.ID(), other, uploadedBytes); err != nil {
		t.Fatalf("another name once the first is done: %v", err)
	}
	// Un colis plein : huit fichiers, jamais un de plus.
	for number := 0; number < 8; number++ {
		name := "plein-" + strings.Repeat("a", number) + "-20261021-030000.tar.age"
		if err := receive(outbox, producerID, upload.ID(), name, uploadedBytes); err != nil {
			break
		}
	}
	if err := receive(outbox, producerID, upload.ID(), "plein-z-20261021-030000.tar.age", uploadedBytes); !errors.Is(err, action.ErrUploadConflict) {
		t.Fatalf("a full parcel still receives: %v", err)
	}
	if got := len(upload.Files()); got != 8 {
		t.Fatalf("the parcel holds %d files, want 8", got)
	}
}

// Une coupure en plein envoi libère le colis et ne laisse pas de .part : l'agent
// recommence avec le même nom.
func TestOutbox_ReceiveFreesTheParcelWhenTheUploadIsCut(t *testing.T) {
	outbox, root, _ := newOutbox(t)
	upload, err := outbox.CreateForUpload(machineID, producerID)
	if err != nil {
		t.Fatal(err)
	}
	cut := errors.New("connection reset")
	reader := io.MultiReader(strings.NewReader(uploadedBytes[:5]), readerFunc(func([]byte) (int, error) { return 0, cut }))
	if err := outbox.Receive(producerID, upload.ID(), uploadedName, reader, int64(len(uploadedBytes)), sumOf(uploadedBytes)); !errors.Is(err, cut) {
		t.Fatalf("a cut upload: %v", err)
	}
	if entries, _ := fs.ReadDir(root.FS(), "outgoing/"+upload.ID()); len(entries) != 0 {
		t.Fatalf("a cut upload left %d files", len(entries))
	}
	if err := receive(outbox, producerID, upload.ID(), uploadedName, uploadedBytes); err != nil {
		t.Fatalf("the retry: %v", err)
	}
}

// Le balayage retire un colis téléversé échu, avec ce qu'il porte.
func TestOutbox_SweepRemovesAnExpiredUpload(t *testing.T) {
	outbox, root, now := newOutbox(t)
	upload, err := outbox.CreateForUpload(machineID, producerID)
	if err != nil {
		t.Fatal(err)
	}
	if err := receive(outbox, producerID, upload.ID(), uploadedName, uploadedBytes); err != nil {
		t.Fatal(err)
	}
	*now = benchNow.Add(action.ParcelLifetime)
	outbox.Sweep()
	if _, err := root.Stat("outgoing/" + upload.ID()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the expired upload is still on the disk: %v", err)
	}
	if files := upload.Files(); files != nil {
		t.Fatalf("an expired upload lists %+v", files)
	}
}
