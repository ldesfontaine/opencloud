package selfupdate

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/ldesfontaine/opencloud/internal/refusal"
)

const checksumsAssetName = "SHA256SUMS"

// Options : ce que l'opérateur demande.
type Options struct {
	// La version compilée dans le binaire.
	CurrentVersion string
	// Le binaire à remplacer, liens symboliques résolus.
	ExecutablePath string
	// Vide : la dernière release.
	RequestedVersion string
	// Dire ce qui est disponible sans rien modifier.
	CheckOnly bool
}

// Result dit ce qui a été fait, pour que l'appelant vérifie ensuite que le
// service répond en nouvelle version.
type Result struct {
	Updated      bool
	Version      Version
	PreviousPath string
	Restarted    bool
}

// Updater enchaîne les étapes de 20-installation-et-mise-a-jour.md et dit
// chacune à l'opérateur, ligne par ligne.
type Updater struct {
	client   *Client
	verifier attestationVerifier
	restart  func(context.Context) error
	out      io.Writer
}

func New(client *Client, out io.Writer) *Updater {
	return &Updater{client: client, verifier: sigstoreVerifier{}, restart: RestartUnit, out: out}
}

// Run télécharge, vérifie, remplace et redémarre — ou refuse, en disant
// pourquoi et quoi faire. Un refus est une refusal.Refusal, pas une erreur.
func (u *Updater) Run(ctx context.Context, options Options) (Result, error) {
	current, err := ParseVersion(options.CurrentVersion)
	if err != nil {
		return Result{}, refusal.Refusal{
			Cause:  fmt.Sprintf("la version installée (%s) n'est pas une release publiée", options.CurrentVersion),
			Remedy: "installer le paquet .deb d'une release, puis relancer self-update",
		}
	}
	u.say("version installée : %s", current)

	// Le verrou est pris avant le premier appel réseau et tenu jusqu'après le
	// redémarrage : sinon un second self-update lancé pendant le
	// téléchargement remplace le .prev par le binaire déjà mis à jour.
	// --check ne modifie rien : il n'a pas à verrouiller.
	if !options.CheckOnly {
		lock, err := lockUpdate(options.ExecutablePath)
		if err != nil {
			return Result{}, describeLockError(err, options.ExecutablePath)
		}
		defer lock.release()
	}

	release, err := u.chooseRelease(ctx, options.RequestedVersion)
	if err != nil {
		return Result{}, err
	}
	u.say("release visée     : %s", release.Version)

	switch {
	case release.Version.Compare(current) == 0:
		u.say("déjà à jour : rien à faire")
		return Result{Version: current}, nil
	case release.Version.Compare(current) < 0:
		return Result{}, refusal.Refusal{
			Cause:  fmt.Sprintf("la release visée (%s) est plus ancienne que la version installée (%s)", release.Version, current),
			Remedy: "self-update ne revient pas en arrière ; remettre opencloud.prev à la main si c'est voulu",
		}
	case !release.Version.IsSequentialUpgradeFrom(current):
		return Result{}, u.refuseVersionJump(ctx, current, release.Version)
	}

	if options.CheckOnly {
		u.say("vérification seule : %s est installable, rien n'est modifié", release.Version)
		return Result{Version: release.Version}, nil
	}

	content, err := u.downloadAndVerify(ctx, release)
	if err != nil {
		return Result{}, err
	}

	previousPath, err := replaceExecutable(options.ExecutablePath, content)
	if errors.Is(err, ErrReplacedButNotSynced) {
		u.say("avertissement     : %v", err)
		err = nil
	}
	if errors.Is(err, fs.ErrPermission) {
		return Result{}, refuseMissingPrivileges(options.ExecutablePath)
	}
	if errors.Is(err, ErrUpdateInProgress) {
		return Result{}, refuseUpdateInProgress(options.ExecutablePath + pendingSuffix)
	}
	if err != nil {
		return Result{}, fmt.Errorf("remplacer le binaire : %w", err)
	}
	u.say("ancien binaire    : %s", previousPath)
	u.say("nouveau binaire   : %s", options.ExecutablePath)
	u.say("retour arrière    : sudo mv %s %s && sudo systemctl restart opencloud", previousPath, options.ExecutablePath)

	restarted, err := u.restartUnit(ctx)
	if err != nil {
		return Result{}, err
	}
	return Result{Updated: true, Version: release.Version, PreviousPath: previousPath, Restarted: restarted}, nil
}

func describeLockError(err error, executablePath string) error {
	if errors.Is(err, ErrUpdateInProgress) {
		return refuseUpdateInProgress(executablePath + lockSuffix)
	}
	// Le verrou se pose dans le dossier du binaire : sans sudo, il n'y a rien
	// à télécharger, autant le dire tout de suite.
	if errors.Is(err, fs.ErrPermission) {
		return refuseMissingPrivileges(executablePath)
	}
	return fmt.Errorf("poser le verrou de mise à jour : %w", err)
}

func refuseUpdateInProgress(lockPath string) refusal.Refusal {
	return refusal.Refusal{
		Cause:  "une mise à jour est déjà en cours, ou une précédente a été interrompue",
		Remedy: fmt.Sprintf("attendre qu'elle finisse, ou retirer %s si plus rien ne tourne", lockPath),
	}
}

func refuseMissingPrivileges(executablePath string) refusal.Refusal {
	return refusal.Refusal{
		Cause:  fmt.Sprintf("droits insuffisants pour remplacer %s", executablePath),
		Remedy: "relancer avec sudo",
	}
}

func (u *Updater) chooseRelease(ctx context.Context, requested string) (Release, error) {
	if requested == "" {
		release, err := u.client.LatestRelease(ctx)
		if errors.Is(err, ErrNotFound) {
			return Release{}, refusal.Refusal{Cause: "aucune release publiée", Remedy: "rien à installer pour l'instant"}
		}
		return release, describeGitHubError(err)
	}

	wanted, err := ParseVersion(requested)
	if err != nil {
		return Release{}, refusal.Refusal{
			Cause:  fmt.Sprintf("%q n'est pas une version X.Y.Z", requested),
			Remedy: "demander un tag de release, par exemple --version v0.1.0",
		}
	}
	release, err := u.client.ReleaseByTag(ctx, wanted.Tag())
	if errors.Is(err, ErrNotFound) {
		return Release{}, refusal.Refusal{
			Cause:  fmt.Sprintf("la release %s n'existe pas", wanted.Tag()),
			Remedy: "vérifier le tag sur GitHub, ou laisser self-update choisir la dernière",
		}
	}
	if errors.Is(err, ErrNotPublished) {
		return Release{}, refusal.Refusal{
			Cause:  fmt.Sprintf("la release %s est un brouillon ou une préversion", wanted.Tag()),
			Remedy: "demander une release publiée, ou laisser self-update choisir la dernière",
		}
	}
	return release, describeGitHubError(err)
}

// refuseVersionJump nomme l'étape à installer d'abord : la plus haute release
// atteignable depuis la version courante.
func (u *Updater) refuseVersionJump(ctx context.Context, current, target Version) error {
	cause := fmt.Sprintf("passer de %s à %s saute au moins une version mineure ; le chemin est séquentiel, chaque version migre depuis la précédente", current, target)

	releases, err := u.client.ListReleases(ctx)
	if err != nil {
		return refusal.Refusal{Cause: cause, Remedy: "installer d'abord la série mineure suivante"}
	}
	step, found := highestSequentialUpgrade(releases, current)
	if !found {
		return refusal.Refusal{Cause: cause, Remedy: "aucune release intermédiaire publiée : installer le paquet .deb de la version suivante à la main"}
	}
	return refusal.Refusal{
		Cause:  cause,
		Remedy: fmt.Sprintf("installer d'abord %s : sudo opencloud self-update --version %s", step, step.Tag()),
	}
}

func highestSequentialUpgrade(releases []Release, current Version) (Version, bool) {
	var best Version
	found := false
	for _, release := range releases {
		if !release.Version.IsSequentialUpgradeFrom(current) {
			continue
		}
		if !found || release.Version.Compare(best) > 0 {
			best = release.Version
			found = true
		}
	}
	return best, found
}

// downloadAndVerify rend le binaire, une fois sa somme et son attestation
// vérifiées. Sans les deux, rien n'est installé.
func (u *Updater) downloadAndVerify(ctx context.Context, release Release) ([]byte, error) {
	binaryName := fmt.Sprintf("opencloud_%s_linux_amd64", release.Version)
	binaryAsset, found := release.Asset(binaryName)
	if !found {
		return nil, refusal.Refusal{
			Cause:  fmt.Sprintf("la release %s ne contient pas %s", release.Version.Tag(), binaryName),
			Remedy: "vérifier la release sur GitHub",
		}
	}
	checksumsAsset, found := release.Asset(checksumsAssetName)
	if !found {
		return nil, refusal.Refusal{
			Cause:  fmt.Sprintf("la release %s ne contient pas %s", release.Version.Tag(), checksumsAssetName),
			Remedy: "vérifier la release sur GitHub",
		}
	}

	checksums, _, err := u.client.DownloadAsset(ctx, checksumsAsset, maxChecksumsBytes)
	if err != nil {
		return nil, describeGitHubError(err)
	}
	expectedDigest, err := findChecksum(checksums, binaryName)
	if err != nil {
		return nil, refusal.Refusal{
			Cause:  fmt.Sprintf("%s ne donne pas la somme de %s", checksumsAssetName, binaryName),
			Remedy: "vérifier la release sur GitHub",
		}
	}

	u.say("téléchargement    : %s (%d Mio)", binaryName, binaryAsset.Size>>20)
	content, digest, err := u.client.DownloadAsset(ctx, binaryAsset, maxBinaryBytes)
	if err != nil {
		return nil, describeGitHubError(err)
	}
	if digest != expectedDigest {
		return nil, refusal.Refusal{
			Cause:  fmt.Sprintf("la somme SHA-256 de %s ne correspond pas à %s", binaryName, checksumsAssetName),
			Remedy: "ne pas installer ; réessayer, puis vérifier la release sur GitHub",
		}
	}
	u.say("somme SHA-256     : vérifiée")

	if err := u.verifyProvenance(ctx, release.Version, binaryName, digest); err != nil {
		return nil, err
	}
	return content, nil
}

func (u *Updater) verifyProvenance(ctx context.Context, version Version, binaryName string, digest [sha256.Size]byte) error {
	bundles, err := u.client.FetchAttestations(ctx, digest)
	if err != nil {
		return describeGitHubError(err)
	}
	if len(bundles) == 0 {
		return refusal.Refusal{
			Cause:  fmt.Sprintf("aucune attestation de provenance pour %s", binaryName),
			Remedy: "ne pas installer ; vérifier la release sur GitHub avant d'aller plus loin",
		}
	}

	identity, err := releaseWorkflowIdentity(u.client.repository, version)
	if err != nil {
		return fmt.Errorf("décrire le signataire attendu : %w", err)
	}
	err = u.verifier.Verify(ctx, bundles, digest, identity)
	if errors.Is(err, ErrTrustRootUnavailable) {
		// Pas pu vérifier n'est pas invalide : une erreur, à réessayer.
		return fmt.Errorf("impossible de vérifier l'attestation : %w", err)
	}
	if err != nil {
		return refusal.Refusal{
			Cause:  fmt.Sprintf("l'attestation de provenance de %s n'est pas valide : %v", binaryName, err),
			Remedy: "ne pas installer ; vérifier la release sur GitHub avant d'aller plus loin",
		}
	}
	u.say("attestation       : vérifiée (workflow release.yml, tag %s)", version.Tag())
	return nil
}

func (u *Updater) restartUnit(ctx context.Context) (bool, error) {
	err := u.restart(ctx)
	if errors.Is(err, ErrNoSystemd) {
		u.say("unité             : pas de systemd ici, relancer le service à la main")
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("redémarrer l'unité : %w", err)
	}
	u.say("unité             : %s redémarrée", unitName)
	return true, nil
}

// describeGitHubError traduit ce que GitHub refuse en refus pour l'opérateur ;
// le reste passe tel quel.
func describeGitHubError(err error) error {
	if errors.Is(err, ErrUnauthorized) {
		return refusal.Refusal{
			Cause:  "GitHub refuse l'accès aux releases",
			Remedy: "tant que le dépôt est privé, renseigner github_token dans la configuration avec un jeton en lecture",
		}
	}
	return err
}

func (u *Updater) say(format string, arguments ...any) {
	fmt.Fprintf(u.out, format+"\n", arguments...)
}
