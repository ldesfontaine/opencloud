package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/fsx"
	"github.com/ldesfontaine/opencloud/internal/release"
)

// ErrUpdated : l'agent a installé une version plus récente et bascule son lien
// current. Run le rend pour que la commande sorte avec le code 3, que systemd
// relance (Restart=on-failure ; seul le code 2 l'en empêche).
var ErrUpdated = errors.New("agent updated: restart")

const (
	// L'agent est relancé par systemd en quelques secondes ; le téléchargement
	// et l'épreuve tiennent largement dans la durée d'un ordre long.
	updateTimeout    = 20 * time.Minute
	selfCheckTimeout = 10 * time.Second
	selfCheckOutput  = 4 << 10
	// Les sommes et leur signature tiennent en quelques centaines d'octets.
	maxSignedFileBytes = 64 << 10
	// Un binaire tient en dizaines de Mio ; au-delà, ce n'est pas le nôtre.
	maxUpdateBinaryBytes = 256 << 20
	incomingPrefix       = ".incoming-"
	currentNewName       = brand.AgentCurrentLink + ".new"
	binaryMode           = 0o755
	incomingDirMode      = 0o700
	binDirMode           = 0o755
)

// updateBinaryName : « opencloud-linux- » et une architecture, rien d'autre.
var updateBinaryName = regexp.MustCompile(`^` + regexp.QuoteMeta(brand.ReleaseBinaryPrefix) + `[a-z0-9]{2,16}$`)

// selfUpdater remplace le binaire que l'agent lance. L'agent tourne depuis
// <état>/bin/current, un lien vers <état>/bin/opencloud-<version> : l'unité
// systemd n'autorise d'écrire que dans l'état, jamais dans /usr. Le serveur ne
// se met jamais à jour ainsi : seul un agent le fait.
type selfUpdater struct {
	stateDir *os.Root
	// La version du binaire qui tourne.
	version string
	// La clé de version que ce binaire embarque ; sans elle, rien ne se vérifie.
	key    release.PublicKey
	hasKey bool
	logger *slog.Logger
	// Ce qui sort du processus, remplaçable par les tests : le chemin du
	// binaire qui tourne, l'épreuve d'une version installée, et la relance.
	executable func() (string, error)
	selfCheck  func(ctx context.Context, binary, version string) error
	restart    func()
}

func newSelfUpdater(stateDir *os.Root, version string, logger *slog.Logger) *selfUpdater {
	return &selfUpdater{
		stateDir: stateDir, version: version, logger: logger,
		executable: os.Executable, selfCheck: runSelfCheck, restart: func() {},
	}
}

// binDir est <état>/bin, en chemin absolu.
func (u *selfUpdater) binDir() (string, error) {
	state, err := filepath.Abs(u.stateDir.Name())
	if err != nil {
		return "", err
	}
	return filepath.Join(state, brand.AgentBinDir), nil
}

// isSelfUpdatable : le binaire qui tourne est dans <état>/bin/ (une fois les
// liens suivis). Un agent installé avant, lancé depuis /usr/local/bin, ne
// pourrait que remplacer un lien que rien ne lance : il refuse.
func (u *selfUpdater) isSelfUpdatable() bool {
	executable, err := u.executable()
	if err != nil {
		return false
	}
	real, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return false
	}
	binDir, err := u.binDir()
	if err != nil {
		return false
	}
	realBinDir, err := filepath.EvalSymlinks(binDir)
	if err != nil {
		return false
	}
	return filepath.Dir(real) == realBinDir
}

// unavailableReason dit pourquoi cet agent ne peut pas se mettre à jour, vide
// s'il le peut : il ne tourne pas depuis son dossier bin/ (not_self_updatable),
// ou son compte ne peut pas y écrire (judgeBin). Le dossier se juge une fois
// ses liens suivis, comme isSelfUpdatable le compare.
func (u *selfUpdater) unavailableReason(judgeBin func(path string) string) string {
	if !u.isSelfUpdatable() {
		return agentwire.ReasonNotSelfUpdatable
	}
	binDir, err := u.binDir()
	if err != nil {
		return agentwire.ReasonPathUnwritable
	}
	realBinDir, err := filepath.EvalSymlinks(binDir)
	if err != nil {
		return agentwire.ReasonPathUnwritable
	}
	return judgeBin(realBinDir)
}

// readAgentUpdate lit les paramètres de agent.update : une version, un colis
// de trois fichiers (sommes, signature, un binaire d'architecture), chacun
// avec sa taille et sa somme. Ce que l'ordre ne dit pas comme il faut est
// refusé avant de télécharger quoi que ce soit.
func readAgentUpdate(body agentwire.OrderBody) (agentwire.AgentUpdateParams, bool) {
	var params agentwire.AgentUpdateParams
	if err := json.Unmarshal(body.Params, &params); err != nil || params.Parcel == "" {
		return params, false
	}
	if _, err := release.ParseVersion(params.Version); err != nil || !updateBinaryName.MatchString(params.Binary) {
		return params, false
	}
	limits := map[string]int64{
		params.Binary: maxUpdateBinaryBytes, brand.ReleaseSumsFile: maxSignedFileBytes, brand.ReleaseSignatureFile: maxSignedFileBytes,
	}
	if len(params.Files) != len(limits) {
		return params, false
	}
	seen := map[string]bool{}
	for _, file := range params.Files {
		limit, expected := limits[file.Name]
		if !expected || seen[file.Name] || file.Size < 0 || file.Size > limit || !isSHA256(file.SHA256) {
			return params, false
		}
		seen[file.Name] = true
	}
	return params, true
}

// update installe la version que l'ordre décrit, et ne rend nil que lorsque le
// lien current pointe sur elle. Dans l'ordre : le colis est téléchargé dans
// bin/.incoming-<version>/ (taille et somme signées par l'ordre) ; la
// signature des sommes est vérifiée avec la clé que CE binaire embarque, pas
// celle du serveur ; la version signée est celle demandée, et pas plus ancienne
// que la sienne ; la somme du binaire est dans les sommes signées ; le binaire
// est installé et éprouvé ; le lien bascule d'un coup. Avant la bascule, tout
// échec laisse l'ancien lien et efface ce qui a été posé.
func (u *selfUpdater) update(ctx context.Context, source parcelSource, params agentwire.AgentUpdateParams) error {
	if !u.hasKey {
		return failWith(agentwire.ReasonReleaseKeyMissing, errors.New("no release key embedded"))
	}
	ctx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()
	bin, err := u.openBin()
	if err != nil {
		return err
	}
	defer bin.Close()
	incomingName := incomingPrefix + params.Version
	incoming, err := openIncoming(bin, incomingName)
	if err != nil {
		return err
	}
	// Le dossier d'arrivée ne survit à aucune issue : réussi, le binaire en est
	// déjà sorti.
	defer func() {
		_ = incoming.Close()
		_ = bin.RemoveAll(incomingName)
	}()
	for _, file := range params.Files {
		if err := downloadTo(ctx, source, incoming, params.Parcel, file, file.Name); err != nil {
			return err
		}
	}
	signed, err := u.verifyDownload(incoming, params)
	if err != nil {
		return err
	}
	target := brand.AgentBinaryPrefix + signed.Version.String()
	previous := currentTarget(bin)
	// Jamais par-dessus le binaire que current lance : une épreuve ratée
	// l'effacerait, et systemd n'aurait plus rien à relancer.
	if target == previous {
		return failWith(agentwire.ReasonDowngrade, fmt.Errorf("%s is the running version", target))
	}
	if err := u.install(ctx, bin, incomingName, target, params); err != nil {
		return err
	}
	if err := switchCurrent(bin, target); err != nil {
		return err
	}
	u.prune(bin, target, previous)
	return nil
}

func (u *selfUpdater) openBin() (*os.Root, error) {
	if err := u.stateDir.MkdirAll(brand.AgentBinDir, binDirMode); err != nil {
		return nil, failWith(agentwire.ReasonPathUnwritable, err)
	}
	bin, err := u.stateDir.OpenRoot(brand.AgentBinDir)
	if err != nil {
		return nil, failWith(agentwire.ReasonPathUnwritable, err)
	}
	return bin, nil
}

// openIncoming repart d'un dossier d'arrivée vide : celui d'une mise à jour
// coupée est jeté.
func openIncoming(bin *os.Root, name string) (*os.Root, error) {
	if err := bin.RemoveAll(name); err != nil {
		return nil, failWith(agentwire.ReasonPathUnwritable, err)
	}
	if err := bin.Mkdir(name, incomingDirMode); err != nil {
		return nil, failWith(agentwire.ReasonPathUnwritable, err)
	}
	incoming, err := bin.OpenRoot(name)
	if err != nil {
		return nil, failWith(agentwire.ReasonPathUnwritable, err)
	}
	return incoming, nil
}

// verifyDownload juge ce qui est arrivé, avec la clé embarquée par ce binaire.
func (u *selfUpdater) verifyDownload(incoming *os.Root, params agentwire.AgentUpdateParams) (release.Release, error) {
	sums, err := incoming.ReadFile(brand.ReleaseSumsFile)
	if err != nil {
		return release.Release{}, failWith(agentwire.ReasonPathUnwritable, err)
	}
	signature, err := incoming.ReadFile(brand.ReleaseSignatureFile)
	if err != nil {
		return release.Release{}, failWith(agentwire.ReasonPathUnwritable, err)
	}
	signed, err := release.VerifySums(sums, signature, u.key)
	if err != nil {
		return release.Release{}, failWith(agentwire.ReasonSignatureInvalid, err)
	}
	if signed.Version.String() != params.Version {
		return release.Release{}, failWith(agentwire.ReasonVersionMismatch, fmt.Errorf("signed %s, asked %s", signed.Version, params.Version))
	}
	// Une version propre illisible (un binaire de développement) compte comme
	// plus ancienne : la mise à jour la corrige. La même version n'a rien à
	// faire : la réinstaller écraserait le binaire qui tourne.
	if own, err := release.ParseVersion(u.version); err == nil && signed.Version.Compare(own) <= 0 {
		return release.Release{}, failWith(agentwire.ReasonDowngrade, fmt.Errorf("%s is not newer than %s", signed.Version, own))
	}
	binary, err := incoming.Open(params.Binary)
	if err != nil {
		return release.Release{}, failWith(agentwire.ReasonPathUnwritable, err)
	}
	defer binary.Close()
	if err := signed.Check(params.Binary, binary); err != nil {
		return release.Release{}, failWith(agentwire.ReasonDigestMismatch, err)
	}
	return signed, nil
}

// install pose le binaire vérifié sous son nom définitif dans bin/, puis
// l'éprouve. Une version qui ne se lance pas est retirée : le lien n'a pas
// bougé, l'agent reste sur l'ancienne.
func (u *selfUpdater) install(ctx context.Context, bin *os.Root, incomingName, target string, params agentwire.AgentUpdateParams) error {
	downloaded := filepath.Join(incomingName, params.Binary)
	if err := bin.Chmod(downloaded, binaryMode); err != nil { // #nosec G302 -- un binaire doit être exécutable
		return failWith(agentwire.ReasonPathUnwritable, err)
	}
	if err := bin.Rename(downloaded, target); err != nil {
		return failWith(agentwire.ReasonPathUnwritable, err)
	}
	if err := fsx.SyncDir(bin, "."); err != nil {
		return failWith(agentwire.ReasonPathUnwritable, err)
	}
	binDir, err := u.binDir()
	if err != nil {
		return failWith(agentwire.ReasonPathUnwritable, err)
	}
	if err := u.selfCheck(ctx, filepath.Join(binDir, target), params.Version); err != nil {
		_ = bin.Remove(target)
		return failWith(agentwire.ReasonSelfCheckFailed, err)
	}
	return nil
}

// currentTarget rend le nom du binaire vers lequel current pointe avant la
// bascule ; vide si le lien manque ou n'est pas un lien. Un lien posé à la
// main en chemin absolu (ln -sfn /var/lib/…/bin/opencloud-v0.0.1) donne le
// même nom : la précédente est gardée pour revenir en arrière.
func currentTarget(bin *os.Root) string {
	target, err := bin.Readlink(brand.AgentCurrentLink)
	if err != nil || target == "" {
		return ""
	}
	return filepath.Base(target)
}

// switchCurrent fait pointer current sur le binaire installé : un lien neuf à
// côté, renommé sur l'ancien, donc atomique. Le dossier est écrit sur le
// disque avant de redémarrer.
func switchCurrent(bin *os.Root, target string) error {
	if err := bin.Remove(currentNewName); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return failWith(agentwire.ReasonPathUnwritable, err)
	}
	if err := bin.Symlink(target, currentNewName); err != nil {
		return failWith(agentwire.ReasonPathUnwritable, err)
	}
	if err := bin.Rename(currentNewName, brand.AgentCurrentLink); err != nil {
		_ = bin.Remove(currentNewName)
		return failWith(agentwire.ReasonPathUnwritable, err)
	}
	if err := fsx.SyncDir(bin, "."); err != nil {
		return failWith(agentwire.ReasonPathUnwritable, err)
	}
	return nil
}

// prune garde la version installée et la précédente (de quoi revenir en
// arrière à la main), efface les autres binaires que l'agent a posés et les
// dossiers d'arrivée d'une mise à jour coupée. Un échec n'en est pas un : la
// bascule est faite.
func (u *selfUpdater) prune(bin *os.Root, keep ...string) {
	entries, err := fs.ReadDir(bin.FS(), ".")
	if err != nil {
		u.logger.Warn("list installed versions", "error", err)
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !isUpdateLeftover(entry, name, keep) {
			continue
		}
		if err := bin.RemoveAll(name); err != nil {
			u.logger.Warn("remove old version", "name", name, "error", err)
		}
	}
}

func isUpdateLeftover(entry fs.DirEntry, name string, keep []string) bool {
	if entry.IsDir() {
		return strings.HasPrefix(name, incomingPrefix)
	}
	version, installed := strings.CutPrefix(name, brand.AgentBinaryPrefix)
	if _, err := release.ParseVersion(version); !installed || err != nil || !entry.Type().IsRegular() {
		return false
	}
	for _, kept := range keep {
		if kept == name {
			return false
		}
	}
	return true
}

// runSelfCheck lance le binaire installé avec « version » et demande qu'il
// dise la version attendue : il s'exécute sur cette machine (architecture,
// bibliothèques), sans rien d'hérité de l'agent. L'environnement est remplacé,
// la sortie bornée, la durée aussi.
func runSelfCheck(ctx context.Context, binary, version string) error {
	ctx, cancel := context.WithTimeout(ctx, selfCheckTimeout)
	defer cancel()
	// bounded: binary est bin/<préfixe>-<version>, que release.ParseVersion a jugée.
	command := exec.CommandContext(ctx, binary, "version") // #nosec G204 G702 -- chemin posé par l'agent sous son état, version jugée par release.ParseVersion
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	command.WaitDelay = time.Second
	var output cappedBuffer
	command.Stdout = &output
	if err := command.Run(); err != nil {
		return fmt.Errorf("run %s version: %w", filepath.Base(binary), err)
	}
	// « opencloud v0.0.2 (commit) » : la commande, puis la version.
	fields := strings.Fields(output.String())
	if len(fields) < 2 || fields[1] != version {
		return fmt.Errorf("%s says %q, want %s", filepath.Base(binary), strings.TrimSpace(output.String()), version)
	}
	return nil
}

// cappedBuffer garde le début d'une sortie et jette le reste sans faire échouer
// l'écriture du processus.
type cappedBuffer struct {
	buffer bytes.Buffer
}

func (c *cappedBuffer) Write(content []byte) (int, error) {
	if room := selfCheckOutput - c.buffer.Len(); room > 0 {
		c.buffer.Write(content[:min(len(content), room)])
	}
	return len(content), nil
}

func (c *cappedBuffer) String() string {
	return c.buffer.String()
}

// acceptAgentUpdate juge agent.update : un agent qui ne tourne pas depuis son
// dossier bin/, ou dont le compte n'y écrit pas, le refuse d'emblée, et l'ordre
// mal formé échoue ; sinon il est accepté et la mise à jour se fait à son tour
// dans le worker. Réussie, elle ne rapporte rien : l'agent s'arrête, et c'est
// son retour avec la nouvelle version que le serveur prend pour la fin de
// l'ordre.
func (r *orderRunner) acceptAgentUpdate(body agentwire.OrderBody) (agentwire.ActionResult, func(ctx context.Context)) {
	// Hors de bin/, jamais : la règle ne dépend pas d'un juge qu'un banc
	// remplace. Puis le compte de l'agent doit pouvoir écrire dans bin/ :
	// refusé avant de télécharger quoi que ce soit.
	reason := agentwire.ReasonNotSelfUpdatable
	if r.updater.isSelfUpdatable() {
		reason = r.checks.agentUpdate()
	}
	if reason != "" {
		r.logger.Warn("order not accepted", "run_id", body.RunID, "kind", string(body.Kind), "reason", reason)
		return agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionRefused, Reason: reason}, nil
	}
	params, readable := readAgentUpdate(body)
	if !readable {
		return agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionFailed, Reason: agentwire.ReasonBadOrder}, nil
	}
	return accepted(body), func(ctx context.Context) {
		if err := r.updater.update(ctx, r.fetch, params); err != nil {
			r.finishLong(body, nil, err)
			return
		}
		r.logger.Info("agent updated, restarting", "run_id", body.RunID, "version", params.Version, "seq", body.Seq)
		r.updater.restart()
	}
}
