package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/release"
)

const testKeyVariable = "TEST_RELEASE_SIGNING_KEY"

// releaseFixture est un dossier de version : un binaire factice, ses sommes,
// et une paire de clés produite par keygen.
type releaseFixture struct {
	dir       string
	binary    string
	sums      string
	keyFile   string
	publicKey release.PublicKey
}

func newReleaseFixture(t *testing.T) releaseFixture {
	t.Helper()
	fixture := releaseFixture{dir: t.TempDir()}
	fixture.binary = filepath.Join(fixture.dir, "tool-linux-amd64")
	if err := os.WriteFile(fixture.binary, []byte("a few bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("a few bytes"))
	fixture.sums = filepath.Join(fixture.dir, "SHA256SUMS")
	if err := os.WriteFile(fixture.sums, []byte(hex.EncodeToString(digest[:])+"  tool-linux-amd64\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.keyFile = filepath.Join(fixture.dir, "release.key")
	var out bytes.Buffer
	if err := releaseCommand([]string{"keygen", "-o", fixture.keyFile}, &out, nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if text, found := strings.CutPrefix(line, "Clé publique : "); found {
			key, err := release.ParsePublicKey(text)
			if err != nil {
				t.Fatal(err)
			}
			fixture.publicKey = key
		}
	}
	return fixture
}

func (fixture releaseFixture) privateKey(t *testing.T) string {
	t.Helper()
	text, err := os.ReadFile(fixture.keyFile)
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

func (fixture releaseFixture) trusted() (release.PublicKey, error) { return fixture.publicKey, nil }

func (fixture releaseFixture) sign(t *testing.T) {
	t.Helper()
	getenv := func(name string) string {
		if name == testKeyVariable {
			return fixture.privateKey(t)
		}
		return ""
	}
	if err := releaseCommand([]string{"sign", "-key-env", testKeyVariable, "-version", "v0.0.2", fixture.sums}, &bytes.Buffer{}, getenv, nil); err != nil {
		t.Fatal(err)
	}
}

func mustRefuse(t *testing.T, err error, want string) {
	t.Helper()
	var refused *refusal
	if !errors.As(err, &refused) {
		t.Fatalf("want a refusal, got %v", err)
	}
	if !strings.Contains(refused.message, want) {
		t.Fatalf("refusal %q does not contain %q", refused.message, want)
	}
}

func TestReleaseSign_WritesAMinisigThatVerifyAccepts(t *testing.T) {
	fixture := newReleaseFixture(t)
	fixture.sign(t)

	info, err := os.Stat(fixture.sums + ".minisig")
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("signature file: %v %v", info, err)
	}
	var out bytes.Buffer
	if err := releaseCommand([]string{"verify", fixture.binary}, &out, nil, fixture.trusted); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("a few bytes"))
	want := "ok v0.0.2 tool-linux-amd64 sha256:" + hex.EncodeToString(digest[:]) + "\n"
	if out.String() != want {
		t.Fatalf("verify printed %q, want %q", out.String(), want)
	}

	// Les mêmes fichiers donnés un par un, depuis un autre dossier.
	moved := t.TempDir()
	for name, path := range map[string]string{"sums": fixture.sums, "sig": fixture.sums + ".minisig"} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(moved, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out.Reset()
	args := []string{"verify", "-sums", filepath.Join(moved, "sums"), "-sig", filepath.Join(moved, "sig"), fixture.binary}
	if err := releaseCommand(args, &out, nil, fixture.trusted); err != nil || out.String() != want {
		t.Fatalf("explicit paths: %q %v", out.String(), err)
	}
}

func TestReleaseVerify_RefusesATamperedBinary(t *testing.T) {
	fixture := newReleaseFixture(t)
	fixture.sign(t)
	if err := os.WriteFile(fixture.binary, []byte("a few bytes, changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := releaseCommand([]string{"verify", fixture.binary}, &out, nil, fixture.trusted)
	mustRefuse(t, err, "n'est pas celle de la version signée")
	if out.Len() != 0 {
		t.Fatalf("nothing must be printed on a refusal: %q", out.String())
	}
}

func TestReleaseVerify_RefusesChangedSumsAndAnotherKey(t *testing.T) {
	fixture := newReleaseFixture(t)
	fixture.sign(t)

	other, _, err := release.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	err = releaseCommand([]string{"verify", fixture.binary}, &bytes.Buffer{}, nil, func() (release.PublicKey, error) { return other, nil })
	mustRefuse(t, err, "pas été faite par la clé de version")

	sums, err := os.ReadFile(fixture.sums)
	if err != nil {
		t.Fatal(err)
	}
	// Le binaire change et ses sommes avec lui : la signature, elle, ne suit pas.
	forged := []byte("0" + string(sums[1:]))
	if sums[0] == '0' {
		forged[0] = '1'
	}
	if err := os.WriteFile(fixture.sums, forged, 0o600); err != nil {
		t.Fatal(err)
	}
	err = releaseCommand([]string{"verify", fixture.binary}, &bytes.Buffer{}, nil, fixture.trusted)
	mustRefuse(t, err, "signature de SHA256SUMS est fausse")
}

func TestReleaseVerify_RefusesAFileNotInTheVersionAndMissingSums(t *testing.T) {
	fixture := newReleaseFixture(t)
	fixture.sign(t)
	renamed := filepath.Join(fixture.dir, "tool")
	if err := os.Rename(fixture.binary, renamed); err != nil {
		t.Fatal(err)
	}
	mustRefuse(t, releaseCommand([]string{"verify", renamed}, &bytes.Buffer{}, nil, fixture.trusted), "n'est pas dans SHA256SUMS")

	alone := filepath.Join(t.TempDir(), "tool-linux-amd64")
	if err := os.WriteFile(alone, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustRefuse(t, releaseCommand([]string{"verify", alone}, &bytes.Buffer{}, nil, fixture.trusted), "côte à côte")
}

func TestReleaseVerify_WithoutEmbeddedKey_Refuses(t *testing.T) {
	fixture := newReleaseFixture(t)
	fixture.sign(t)
	noKey := func() (release.PublicKey, error) { return release.PublicKey{}, release.ErrNoTrustedKey }
	err := releaseCommand([]string{"verify", fixture.binary}, &bytes.Buffer{}, nil, noKey)
	mustRefuse(t, err, "n'embarque pas de clé de version")
}

func TestRunRelease_EmbeddedKeyIsEmptyInThisBuild(t *testing.T) {
	if release.TrustedPublicKey != "" {
		t.Skip("une clé de version est embarquée")
	}
	fixture := newReleaseFixture(t)
	fixture.sign(t)
	mustRefuse(t, runRelease([]string{"verify", fixture.binary}), "n'embarque pas de clé de version")
}

func TestReleaseSign_RefusesAnEmptyKeyVariable(t *testing.T) {
	fixture := newReleaseFixture(t)
	args := []string{"sign", "-key-env", testKeyVariable, "-version", "v0.0.2", fixture.sums}
	empty := func(string) string { return "  \n" }
	mustRefuse(t, releaseCommand(args, &bytes.Buffer{}, empty, nil), testKeyVariable)
	if _, err := os.Stat(fixture.sums + ".minisig"); err == nil {
		t.Fatal("no signature must be written without a key")
	}

	secret := func(string) string { return "this-is-not-a-key" }
	err := releaseCommand(args, &bytes.Buffer{}, secret, nil)
	mustRefuse(t, err, "ne contient pas une clé privée")
	if strings.Contains(err.Error(), "this-is-not-a-key") {
		t.Fatal("the refusal must not repeat the variable")
	}
}

func TestReleaseSign_RefusesABadVersionAndUnreadableSums(t *testing.T) {
	fixture := newReleaseFixture(t)
	getenv := func(string) string { return fixture.privateKey(t) }
	mustRefuse(t, releaseCommand([]string{"sign", "-key-env", testKeyVariable, "-version", "0.0.2", fixture.sums}, &bytes.Buffer{}, getenv, nil), "vX.Y.Z")

	notSums := filepath.Join(fixture.dir, "notes.txt")
	if err := os.WriteFile(notSums, []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustRefuse(t, releaseCommand([]string{"sign", "-key-env", testKeyVariable, "-version", "v0.0.2", notSums}, &bytes.Buffer{}, getenv, nil), "n'est pas un fichier de sommes")
}

func TestReleaseKeygen_RefusesAnExistingFileAndWrites0600(t *testing.T) {
	fixture := newReleaseFixture(t)
	info, err := os.Stat(fixture.keyFile)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("private key file: %v %v", info, err)
	}
	if _, err := release.ParsePrivateKey(fixture.privateKey(t)); err != nil {
		t.Fatalf("keygen wrote something that is not a private key: %v", err)
	}
	before := fixture.privateKey(t)
	var out bytes.Buffer
	mustRefuse(t, releaseCommand([]string{"keygen", "-o", fixture.keyFile}, &out, nil, nil), "existe déjà")
	if fixture.privateKey(t) != before || out.Len() != 0 {
		t.Fatal("an existing key must be left untouched")
	}

	// Ce que l'opérateur lit : la clé publique, jamais la privée, et la suite à donner.
	var printed bytes.Buffer
	otherKeyFile := filepath.Join(t.TempDir(), "other.key")
	if err := releaseCommand([]string{"keygen", "-o", otherKeyFile}, &printed, nil, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(printed.String(), "RELEASE_SIGNING_KEY") || !strings.Contains(printed.String(), "internal/release/key.go") {
		t.Fatalf("keygen advice: %q", printed.String())
	}
	written, err := os.ReadFile(otherKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(printed.String(), strings.TrimSpace(string(written))) {
		t.Fatal("keygen must not print the private key")
	}
}

func TestReleaseCommand_RefusesAMissingOrUnknownSubcommand(t *testing.T) {
	mustRefuse(t, releaseCommand(nil, &bytes.Buffer{}, nil, nil), "sous-commande manquante")
	mustRefuse(t, releaseCommand([]string{"publish"}, &bytes.Buffer{}, nil, nil), "sous-commande inconnue")
	mustRefuse(t, releaseCommand([]string{"verify"}, &bytes.Buffer{}, nil, func() (release.PublicKey, error) { return release.PublicKey{}, nil }), "verify attend un fichier")
}
