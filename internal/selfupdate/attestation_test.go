package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

// Les fixtures sont de vraies attestations de la release v0.4.52 de Plumber
// (getplumber/plumber, fichier plumber-linux-amd64), lues par l'API GitHub le
// 6 septembre 2026, et les racines de confiance des deux instances Sigstore
// obtenues par TUF le même jour. Elles se vérifient hors ligne : la datation
// vient des bundles, pas de l'horloge.
const plumberDigestHex = "fb0943f49634da7678456ab428fb87e884c23af0b457d8ce9854eaa3f27700f7"

func plumberDigest(t *testing.T) [sha256.Size]byte {
	t.Helper()
	var digest [sha256.Size]byte
	decoded, err := hex.DecodeString(plumberDigestHex)
	if err != nil {
		t.Fatal(err)
	}
	copy(digest[:], decoded)
	return digest
}

func loadTrustedRoot(t *testing.T, name string) *root.TrustedRoot {
	t.Helper()
	content, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	trusted, err := root.NewTrustedRootFromJSON(content)
	if err != nil {
		t.Fatal(err)
	}
	return trusted
}

func loadBundle(t *testing.T, name string) []byte {
	t.Helper()
	content, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func TestParseBundle_TellsWhichInstanceSigned(t *testing.T) {
	_, instance, err := parseBundle(loadBundle(t, "plumber-workflow-bundle.json"))
	if err != nil || instance != publicGoodInstance {
		t.Fatalf("attestation du workflow : instance %v, err %v", instance, err)
	}
	_, instance, err = parseBundle(loadBundle(t, "plumber-github-bundle.json"))
	if err != nil || instance != githubInstance {
		t.Fatalf("attestation de GitHub : instance %v, err %v", instance, err)
	}
	if _, _, err := parseBundle([]byte(`{"not":"a bundle"}`)); err == nil {
		t.Fatal("un faux bundle doit être refusé")
	}
}

func TestVerifyAttestation_WorkflowBundle_PassesAgainstPublicRoot(t *testing.T) {
	trusted := loadTrustedRoot(t, "sigstore-trusted-root.json")
	parsed, instance, err := parseBundle(loadBundle(t, "plumber-workflow-bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	identity, err := verify.NewShortCertificateIdentity(actionsIssuer, "",
		"https://github.com/getplumber/plumber/.github/workflows/release.yml@refs/heads/main", "")
	if err != nil {
		t.Fatal(err)
	}

	if err := verifyAttestation(parsed, trusted, instance, plumberDigest(t), identity); err != nil {
		t.Fatalf("l'attestation du workflow doit passer : %v", err)
	}

	wrongDigest := plumberDigest(t)
	wrongDigest[0] ^= 0xff
	if err := verifyAttestation(parsed, trusted, instance, wrongDigest, identity); err == nil {
		t.Fatal("une autre somme doit être refusée")
	}
	otherWorkflow, _ := verify.NewShortCertificateIdentity(actionsIssuer, "",
		"https://github.com/getplumber/plumber/.github/workflows/ci.yml@refs/heads/main", "")
	if err := verifyAttestation(parsed, trusted, instance, plumberDigest(t), otherWorkflow); err == nil {
		t.Fatal("un autre workflow doit être refusé")
	}
}

func TestVerifyAttestation_GitHubBundle_PassesAgainstGitHubRoot(t *testing.T) {
	trusted := loadTrustedRoot(t, "github-trusted-root.json")
	parsed, instance, err := parseBundle(loadBundle(t, "plumber-github-bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	// L'attestation que GitHub signe lui-même pour une release n'a pas
	// d'émetteur OIDC : on l'accepte vide, ici seulement.
	identity, err := verify.NewShortCertificateIdentity("", "^$", "https://dotcom.releases.github.com", "")
	if err != nil {
		t.Fatal(err)
	}

	if err := verifyAttestation(parsed, trusted, instance, plumberDigest(t), identity); err != nil {
		t.Fatalf("l'attestation de GitHub doit passer : %v", err)
	}
	if err := verifyAttestation(parsed, loadTrustedRoot(t, "sigstore-trusted-root.json"), instance, plumberDigest(t), identity); err == nil {
		t.Fatal("la racine de l'autre instance doit la refuser")
	}
}

func TestReleaseWorkflowIdentity_NamesRepositoryWorkflowAndTag(t *testing.T) {
	identity, err := releaseWorkflowIdentity(Repository, Version{Major: 0, Minor: 0, Patch: 2})
	if err != nil {
		t.Fatal(err)
	}
	want := "https://github.com/ldesfontaine/opencloud/.github/workflows/release.yml@refs/tags/v0.0.2"
	if identity.SubjectAlternativeName.SubjectAlternativeName != want || identity.Issuer.Issuer != actionsIssuer {
		t.Fatalf("identité = %+v", identity)
	}
}
