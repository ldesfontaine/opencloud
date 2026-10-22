package release

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/brand"
)

// importFixture est un dossier de version tel que la page des versions le
// donne : deux binaires, leurs sommes et la signature, faits avec une clé de
// test tirée ici.
type importFixture struct {
	dir        string
	version    Version
	publicKey  PublicKey
	privateKey PrivateKey
	binaries   map[string][]byte
}

func newImportFixture(t *testing.T, version Version) importFixture {
	t.Helper()
	publicKey, privateKey, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	fixture := importFixture{
		dir: t.TempDir(), version: version, publicKey: publicKey, privateKey: privateKey,
		binaries: map[string][]byte{
			BinaryName("amd64"): []byte("an amd64 binary"),
			BinaryName("arm64"): []byte("an arm64 binary"),
		},
	}
	fixture.writeSigned(t)
	return fixture
}

// writeSigned écrit les binaires, leurs sommes et la signature.
func (fixture importFixture) writeSigned(t *testing.T) {
	t.Helper()
	var sums string
	for _, name := range []string{BinaryName("amd64"), BinaryName("arm64")} {
		content := fixture.binaries[name]
		digest := sha256.Sum256(content)
		sums += hex.EncodeToString(digest[:]) + "  " + name + "\n"
		writeFile(t, filepath.Join(fixture.dir, name), content)
	}
	writeFile(t, filepath.Join(fixture.dir, brand.ReleaseSumsFile), []byte(sums))
	signature, err := Sign([]byte(sums), fixture.privateKey, "timestamp:1\tfile:"+brand.ReleaseSumsFile+"\tversion:"+fixture.version.String())
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(fixture.dir, brand.ReleaseSignatureFile), signature)
}

func writeFile(t *testing.T, name string, content []byte) {
	t.Helper()
	if err := os.WriteFile(name, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func openState(t *testing.T) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root
}

func TestImport_CopiesAVerifiedRelease(t *testing.T) {
	fixture := newImportFixture(t, Version{0, 0, 2})
	state := openState(t)
	library := NewLibrary(state, fixture.publicKey)
	signed, err := library.Import(fixture.dir)
	if err != nil {
		t.Fatal(err)
	}
	if signed.Version != (Version{0, 0, 2}) {
		t.Fatalf("version %v", signed.Version)
	}
	artifact, err := library.Artifact(Version{0, 0, 2}, "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Name != "opencloud-linux-arm64" || artifact.Path != "releases/v0.0.2/opencloud-linux-arm64" ||
		artifact.SumsPath != "releases/v0.0.2/SHA256SUMS" || artifact.SignaturePath != "releases/v0.0.2/SHA256SUMS.minisig" ||
		artifact.Size != int64(len("an arm64 binary")) || artifact.Version != (Version{0, 0, 2}) {
		t.Fatalf("artifact %+v", artifact)
	}
	info, err := state.Stat(artifact.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("binary mode %v", info.Mode().Perm())
	}
}

func TestImport_RefusesABadSignatureAndLeavesNothing(t *testing.T) {
	fixture := newImportFixture(t, Version{0, 0, 2})
	otherKey, _, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(t *testing.T) *Library{
		"another key": func(t *testing.T) *Library { return NewLibrary(openState(t), otherKey) },
		"a binary changed after the signature": func(t *testing.T) *Library {
			writeFile(t, filepath.Join(fixture.dir, BinaryName("amd64")), []byte("not the signed one"))
			return NewLibrary(openState(t), fixture.publicKey)
		},
		"a listed binary missing": func(t *testing.T) *Library {
			if err := os.Remove(filepath.Join(fixture.dir, BinaryName("arm64"))); err != nil {
				t.Fatal(err)
			}
			return NewLibrary(openState(t), fixture.publicKey)
		},
	}
	for name, build := range cases {
		library := build(t)
		if _, err := library.Import(fixture.dir); err == nil {
			t.Errorf("%s: imported", name)
		}
		if _, err := library.root.Stat("releases"); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s: something was left in the state (%v)", name, err)
		}
	}
}

func TestImport_TwiceIsHarmless_ButADifferentFileIsRefused(t *testing.T) {
	fixture := newImportFixture(t, Version{0, 0, 2})
	library := NewLibrary(openState(t), fixture.publicKey)
	if _, err := library.Import(fixture.dir); err != nil {
		t.Fatal(err)
	}
	before, err := library.root.Stat("releases/v0.0.2/opencloud-linux-amd64")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := library.Import(fixture.dir); err != nil {
		t.Fatalf("second import: %v", err)
	}
	after, err := library.root.Stat("releases/v0.0.2/opencloud-linux-amd64")
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("the second import rewrote a file that did not change")
	}

	// La même version, signée avec d'autres sommes (par la même clé).
	other := fixture
	other.dir = t.TempDir()
	other.binaries = map[string][]byte{BinaryName("amd64"): []byte("another amd64"), BinaryName("arm64"): []byte("another arm64")}
	other.writeSigned(t)
	if _, err := library.Import(other.dir); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("other sums under the same version: %v", err)
	}
	if _, err := library.Artifact(Version{0, 0, 2}, "amd64"); err != nil {
		t.Fatalf("the first import must stay usable: %v", err)
	}
}

func TestImport_ACutImportIsCompletedByTheNext(t *testing.T) {
	fixture := newImportFixture(t, Version{0, 0, 2})
	library := NewLibrary(openState(t), fixture.publicKey)
	if _, err := library.Import(fixture.dir); err != nil {
		t.Fatal(err)
	}
	// Une coupure avant les sommes : un binaire manque, SHA256SUMS aussi.
	for _, name := range []string{BinaryName("arm64"), brand.ReleaseSumsFile} {
		if err := library.root.Remove("releases/v0.0.2/" + name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := library.Artifact(Version{0, 0, 2}, "amd64"); !errors.Is(err, ErrNoRelease) {
		t.Fatalf("a cut import is not a version: %v", err)
	}
	if _, err := library.Import(fixture.dir); err != nil {
		t.Fatal(err)
	}
	if _, err := library.Artifact(Version{0, 0, 2}, "arm64"); err != nil {
		t.Fatal(err)
	}
}

func TestArtifact_ReverifiesAfterImport(t *testing.T) {
	fixture := newImportFixture(t, Version{0, 0, 2})
	library := NewLibrary(openState(t), fixture.publicKey)
	if _, err := library.Import(fixture.dir); err != nil {
		t.Fatal(err)
	}
	if err := library.root.WriteFile("releases/v0.0.2/opencloud-linux-amd64", []byte("an amd64 binarY"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := library.Artifact(Version{0, 0, 2}, "amd64"); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("a changed byte: %v", err)
	}
	if _, err := library.Artifact(Version{0, 0, 2}, "arm64"); err != nil {
		t.Fatalf("the other binary is untouched: %v", err)
	}

	// Les sommes changées après l'import ne passent plus la signature.
	if err := library.root.WriteFile("releases/v0.0.2/SHA256SUMS", []byte(sumsLine("x")), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := library.Artifact(Version{0, 0, 2}, "arm64"); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("changed sums: %v", err)
	}
}

func TestArtifact_NotImportedOrNotPublishedForThisArch(t *testing.T) {
	fixture := newImportFixture(t, Version{0, 0, 2})
	library := NewLibrary(openState(t), fixture.publicKey)
	if _, err := library.Artifact(Version{0, 0, 2}, "amd64"); !errors.Is(err, ErrNoRelease) {
		t.Fatalf("nothing imported: %v", err)
	}
	if _, err := library.Import(fixture.dir); err != nil {
		t.Fatal(err)
	}
	for _, arch := range []string{"riscv64", "", "../amd64"} {
		if _, err := library.Artifact(Version{0, 0, 2}, arch); !errors.Is(err, ErrNoRelease) {
			t.Errorf("arch %q: %v", arch, err)
		}
	}
	if _, err := library.Artifact(Version{0, 0, 3}, "amd64"); !errors.Is(err, ErrNoRelease) {
		t.Fatalf("another version: %v", err)
	}
}

func TestArtifact_RefusesAFolderThatHoldsAnotherVersion(t *testing.T) {
	fixture := newImportFixture(t, Version{0, 0, 3})
	library := NewLibrary(openState(t), fixture.publicKey)
	if _, err := library.Import(fixture.dir); err != nil {
		t.Fatal(err)
	}
	// Le dossier v0.0.2 reçoit les fichiers signés de v0.0.3 : le dossier
	// nomme la version, la signature la dit.
	if err := library.root.Rename("releases/v0.0.3", "releases/v0.0.2"); err != nil {
		t.Fatal(err)
	}
	if _, err := library.Artifact(Version{0, 0, 2}, "amd64"); !errors.Is(err, ErrMalformed) {
		t.Fatalf("renamed folder: %v", err)
	}
}

func sumsLine(name string) string {
	digest := sha256.Sum256([]byte(name))
	return hex.EncodeToString(digest[:]) + "  " + name + "\n"
}
