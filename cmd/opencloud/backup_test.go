package main

import (
	"archive/tar"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/ldesfontaine/opencloud/internal/brand"
)

// La phrase de test : des mots, pas un secret.
// #nosec G101 -- une phrase d'essai.
const restorePassphrase = "une phrase de test assez longue" // gitleaks:allow

// restoreBench fabrique ce que la nuit dépose : une archive age de trois
// fichiers, et la clé privée scellée par la phrase.
type restoreBench struct {
	archive string
	key     string
	files   map[string]string
}

func newRestoreBench(t *testing.T, entries ...string) *restoreBench {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	b := &restoreBench{files: map[string]string{
		brand.DatabaseFile:  "une base de test",
		brand.SecretKeyFile: "une clé de test",
		brand.ServerKeyFile: "une autre clé de test",
	}}
	if len(entries) == 0 {
		entries = []string{brand.DatabaseFile, brand.SecretKeyFile, brand.ServerKeyFile}
	}
	var packed bytes.Buffer
	encrypter, err := age.Encrypt(&packed, identity.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	archive := tar.NewWriter(encrypter)
	for _, name := range entries {
		content := b.files[name]
		if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := encrypter.Close(); err != nil {
		t.Fatal(err)
	}

	var sealed bytes.Buffer
	scrypt, err := age.NewScryptRecipient(restorePassphrase)
	if err != nil {
		t.Fatal(err)
	}
	scrypt.SetWorkFactor(10)
	sealer, err := age.Encrypt(&sealed, scrypt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sealer.Write([]byte(identity.String() + "\n")); err != nil {
		t.Fatal(err)
	}
	if err := sealer.Close(); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	b.archive = filepath.Join(dir, "sauvegarde.tar.age")
	b.key = filepath.Join(dir, "cle.age")
	if err := os.WriteFile(b.archive, packed.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b.key, sealed.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return b
}

func (b *restoreBench) restore(state, phrase string) (string, string, error) {
	var stdout, stderr bytes.Buffer
	err := backupCommand([]string{"restore", "-key", b.key, "-state", state, b.archive}, strings.NewReader(phrase), &stdout, &stderr, false)
	return stdout.String(), stderr.String(), err
}

func TestBackupRestore_ExtractsIntoAnEmptyDirectoryAndRefusesAFullOne(t *testing.T) {
	b := newRestoreBench(t)
	state := filepath.Join(t.TempDir(), "etat")

	stdout, _, err := b.restore(state, restorePassphrase+"\n")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range b.files {
		content, err := os.ReadFile(filepath.Join(state, name))
		info, statErr := os.Stat(filepath.Join(state, name))
		if err != nil || string(content) != want || statErr != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("%s: %q %v", name, content, err)
		}
	}
	if info, err := os.Stat(state); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("state dir %v %v", info, err)
	}
	if !strings.Contains(stdout, "chown") || !strings.Contains(stdout, brand.ServerUnit) {
		t.Fatalf("the next steps are missing:\n%s", stdout)
	}

	// Le dossier porte maintenant une base : une seconde restauration la
	// détruirait, elle est refusée avant de lire quoi que ce soit.
	_, _, err = b.restore(state, restorePassphrase+"\n")
	var refused *refusal
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), "n'est pas vide") {
		t.Fatalf("a full directory: %v", err)
	}
	// Un dossier vide, déjà là, convient.
	empty := t.TempDir()
	if _, _, err := b.restore(empty, restorePassphrase+"\r\n"); err != nil {
		t.Fatalf("an empty directory: %v", err)
	}
}

func TestBackupRestore_WrongPassphraseOrBrokenArchiveLeavesNothing(t *testing.T) {
	b := newRestoreBench(t)
	state := filepath.Join(t.TempDir(), "etat")
	var refused *refusal
	if _, _, err := b.restore(state, "une autre phrase de test longue\n"); !errors.As(err, &refused) || !strings.Contains(err.Error(), "n'ouvre pas") {
		t.Fatalf("wrong passphrase: %v", err)
	}
	if _, _, err := b.restore(state, ""); !errors.As(err, &refused) {
		t.Fatalf("no passphrase: %v", err)
	}
	if _, err := os.Stat(state); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a wrong passphrase created the directory: %v", err)
	}

	// Une archive qui porte un fichier de trop est refusée en entier.
	b.files["../evil"] = "x"
	odd := newRestoreBench(t, brand.DatabaseFile, "../evil")
	odd.files = b.files
	if _, _, err := odd.restore(state, restorePassphrase+"\n"); !errors.As(err, &refused) {
		t.Fatalf("unexpected entry: %v", err)
	}
	entries, _ := os.ReadDir(state)
	for _, entry := range entries {
		t.Errorf("left behind: %s", entry.Name())
	}
}

func TestBackupCommand_ShowsItsUsage(t *testing.T) {
	var refused *refusal
	for _, args := range [][]string{nil, {"unknown"}, {"restore"}, {"restore", "-key", "k", "-state", "s"}} {
		if err := backupCommand(args, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, false); !errors.As(err, &refused) {
			t.Errorf("%v: %v", args, err)
		}
	}
}

func TestReadPassphrase_PromptsOnlyOnATerminal(t *testing.T) {
	var stderr bytes.Buffer
	phrase, err := readPassphrase(strings.NewReader("une phrase\nla suite\n"), &stderr, false)
	if err != nil || phrase != "une phrase" || stderr.Len() != 0 {
		t.Fatalf("%q %v %q", phrase, err, stderr.String())
	}
	if _, err := readPassphrase(strings.NewReader("une phrase"), &stderr, true); err != nil || stderr.Len() == 0 {
		t.Fatalf("terminal: %v %q", err, stderr.String())
	}
}
