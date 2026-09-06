package selfupdate

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/verify"
	"github.com/theupdateframework/go-tuf/v2/metadata/fetcher"
)

// L'attestation de provenance (SLSA, signée par Sigstore) que le workflow
// release dépose sur GitHub pour chaque fichier publié prouve que le fichier
// téléchargé a été construit par ce workflow, sur ce dépôt, pour ce tag — pas
// seulement qu'il est arrivé intact.
//
// Deux instances Sigstore signent selon la visibilité du dépôt : celle de
// GitHub (dépôt privé : horodatage signé, pas de journal public) et l'instance
// publique (dépôt public : journal de transparence Rekor). On reconnaît
// l'instance à l'émetteur du certificat, comme le fait gh.
//
// La vérification elle-même est déléguée à sigstore-go, la bibliothèque de gh.
// C'est la seule dépendance lourde du binaire, assumée : réécrire cette
// vérification à la main serait la vraie faute.

// Racine TUF de l'instance Sigstore de GitHub, copiée de gh (dépôt cli/cli,
// pkg/cmd/attestation/verification/embed/tuf-repo.github.com/root.json,
// commit 4618a267 du 15 août 2024, SHA-256 98cba97b…de1a8d). TUF suit les
// rotations à partir de cette racine ; celle de l'instance publique est
// embarquée dans sigstore-go.
//
//go:embed trust/github-tuf-root.json
var githubTUFRoot []byte

const (
	githubTUFMirror = "https://tuf-repo.github.com"

	// L'organisation de l'émetteur du certificat dit quelle instance a signé.
	githubIssuerOrganization     = "GitHub, Inc."
	publicGoodIssuerOrganization = "sigstore.dev"

	// L'émetteur OIDC de GitHub Actions et le workflow attendu, tels qu'ils
	// figurent dans le certificat.
	actionsIssuer       = "https://token.actions.githubusercontent.com"
	releaseWorkflowPath = ".github/workflows/release.yml"

	// Bornes de la récupération TUF : par requête, et deux reprises au plus.
	trustRootRequestTimeout = 30 * time.Second
	trustRootRetryInterval  = 2 * time.Second
	trustRootRetryCount     = 2
)

var (
	// ErrUnknownSigstoreInstance : le certificat ne vient d'aucune des deux instances.
	ErrUnknownSigstoreInstance = errors.New("unknown sigstore instance")
	// ErrTrustRootUnavailable : la racine de confiance n'a pas pu être obtenue —
	// on n'a pas pu vérifier, ce qui n'est pas la même chose qu'invalide.
	ErrTrustRootUnavailable = errors.New("trust root unavailable")
)

type sigstoreInstance int

const (
	githubInstance sigstoreInstance = iota
	publicGoodInstance
)

// releaseWorkflowIdentity est le seul signataire accepté : le workflow release
// de ce dépôt, lancé sur le tag de la version demandée.
func releaseWorkflowIdentity(repository string, version Version) (verify.CertificateIdentity, error) {
	workflowURI := fmt.Sprintf("https://github.com/%s/%s@refs/tags/%s", repository, releaseWorkflowPath, version.Tag())
	return verify.NewShortCertificateIdentity(actionsIssuer, "", workflowURI, "")
}

// attestationVerifier est ce que l'Updater attend ; le vrai est
// sigstoreVerifier, les tests en passent un faux.
type attestationVerifier interface {
	Verify(ctx context.Context, bundles [][]byte, digest [sha256.Size]byte, identity verify.CertificateIdentity) error
}

// sigstoreVerifier va chercher la racine de confiance de l'instance qui a
// signé, par TUF, et vérifie chaque bundle jusqu'à en trouver un bon.
type sigstoreVerifier struct{}

func (sigstoreVerifier) Verify(ctx context.Context, bundles [][]byte, digest [sha256.Size]byte, identity verify.CertificateIdentity) error {
	roots := map[sigstoreInstance]*root.TrustedRoot{}
	var refusals []error
	for index, raw := range bundles {
		parsed, instance, err := parseBundle(raw)
		if err != nil {
			refusals = append(refusals, fmt.Errorf("attestation %d: %w", index, err))
			continue
		}

		trusted, known := roots[instance]
		if !known {
			trusted, err = fetchTrustedRoot(instance)
			if err != nil {
				return fmt.Errorf("%w: %w", ErrTrustRootUnavailable, err)
			}
			roots[instance] = trusted
		}

		if err := verifyAttestation(parsed, trusted, instance, digest, identity); err != nil {
			refusals = append(refusals, fmt.Errorf("attestation %d: %w", index, err))
			continue
		}
		return nil
	}
	return errors.Join(refusals...)
}

// parseBundle lit un bundle et dit quelle instance l'a signé.
func parseBundle(raw []byte) (*bundle.Bundle, sigstoreInstance, error) {
	var parsed bundle.Bundle
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, 0, fmt.Errorf("decode bundle: %w", err)
	}

	content, err := parsed.VerificationContent()
	if err != nil {
		return nil, 0, fmt.Errorf("read bundle: %w", err)
	}
	certificate := content.Certificate()
	if certificate == nil {
		return nil, 0, errors.New("bundle carries no certificate")
	}

	for _, organization := range certificate.Issuer.Organization {
		switch organization {
		case githubIssuerOrganization:
			return &parsed, githubInstance, nil
		case publicGoodIssuerOrganization:
			return &parsed, publicGoodInstance, nil
		}
	}
	return nil, 0, fmt.Errorf("%w: issuer %v", ErrUnknownSigstoreInstance, certificate.Issuer.Organization)
}

// fetchTrustedRoot obtient la racine de confiance courante par TUF, sans cache
// disque : self-update est rare, et root n'a rien à laisser dans l'état.
// Le fetcher par défaut de go-tuf n'a ni délai ni contexte (l'option Context
// de sigstore-go n'est pas lue) : on lui donne un client HTTP borné.
func fetchTrustedRoot(instance sigstoreInstance) (*root.TrustedRoot, error) {
	boundedFetcher := fetcher.NewDefaultFetcher()
	boundedFetcher.SetHTTPClient(&http.Client{Timeout: trustRootRequestTimeout})
	boundedFetcher.SetRetry(trustRootRetryInterval, trustRootRetryCount)
	boundedFetcher.SetHTTPUserAgent("opencloud self-update")

	options := tuf.DefaultOptions().WithDisableLocalCache().WithFetcher(boundedFetcher)
	if instance == githubInstance {
		options = options.WithRoot(githubTUFRoot).WithRepositoryBaseURL(githubTUFMirror)
	}

	client, err := tuf.New(options)
	if err != nil {
		return nil, fmt.Errorf("tuf client for %s: %w", options.RepositoryBaseURL, err)
	}
	trustedRootJSON, err := client.GetTarget("trusted_root.json")
	if err != nil {
		return nil, fmt.Errorf("fetch trusted root from %s: %w", options.RepositoryBaseURL, err)
	}
	trusted, err := root.NewTrustedRootFromJSON(trustedRootJSON)
	if err != nil {
		return nil, fmt.Errorf("parse trusted root from %s: %w", options.RepositoryBaseURL, err)
	}
	return trusted, nil
}

// verifyAttestation vérifie un bundle contre une racine de confiance : la
// signature, sa datation, l'identité du signataire et la somme du fichier.
// Les exigences suivent celles de gh pour chaque instance.
func verifyAttestation(parsed *bundle.Bundle, trusted root.TrustedMaterial, instance sigstoreInstance, digest [sha256.Size]byte, identity verify.CertificateIdentity) error {
	var requirements []verify.VerifierOption
	if instance == githubInstance {
		requirements = []verify.VerifierOption{verify.WithSignedTimestamps(1)}
	} else {
		requirements = []verify.VerifierOption{
			verify.WithSignedCertificateTimestamps(1),
			verify.WithTransparencyLog(1),
			verify.WithObserverTimestamps(1),
		}
	}

	verifier, err := verify.NewVerifier(trusted, requirements...)
	if err != nil {
		return fmt.Errorf("build verifier: %w", err)
	}
	policy := verify.NewPolicy(verify.WithArtifactDigest("sha256", digest[:]), verify.WithCertificateIdentity(identity))
	if _, err := verifier.Verify(parsed, policy); err != nil {
		return err
	}
	return nil
}
