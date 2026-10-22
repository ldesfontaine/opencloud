package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/fsx"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

const (
	// Le seul programme que l'agent lance pour déployer : un chemin constant.
	dockerBinary = "/usr/bin/docker"
	// Compose met de l'ordre dans les images à tirer : dix minutes, au-delà
	// l'ordre échoue (le serveur lui laisse une heure).
	composeUpTimeout = 10 * time.Minute
	versionTimeout   = 20 * time.Second
	// Une image qui se tire et se décompresse prend de la place : en dessous
	// de ce libre, rien ne s'écrit.
	minFreeBytes = 2 << 30

	composeFileName = "compose.yml"
	envFileName     = ".env"
	dataDirName     = "data"
	// Les dossiers du service sont lisibles de tous ; le compose aussi ; l'.env
	// est à root seul.
	serviceDirMode  = 0o755
	composeFileMode = 0o644
	envFileMode     = 0o600

	// La sortie de Compose garde ses dernières lignes : un résultat en porte
	// au plus MaxOutputLines, dont quelques-unes sont celles de l'agent.
	agentSummaryLines = 6
	composeTailLines  = agentwire.MaxOutputLines - agentSummaryLines
)

// L'environnement de Compose est remplacé, pas hérité : le PATH minimal, une
// langue fixe, et le HOME de root, où Docker garde les identifiants des
// registres.
var composeEnvironment = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C", "HOME=/root"}

// Compose 2.24 est la première version qui sait ignorer un env_file absent
// (required: false).
var minComposeVersion = [3]int{2, 24, 0}

var composeVersionPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

// errDockerMissing : le programme n'est pas là, ou pas exécutable.
var errDockerMissing = errors.New("docker is not installed")

// composeRunner est ce que l'agent demande à Docker Compose : sa version, et
// de lancer le projet d'un dossier. Les tests le remplacent : aucun test ne
// lance Docker.
type composeRunner interface {
	Version(ctx context.Context) (string, error)
	// Up lance le projet du dossier, sous son nom, et rend les dernières
	// lignes de sa sortie, même quand il échoue.
	Up(ctx context.Context, dir, project string) ([]agentwire.OutputLine, error)
}

// dockerTools est ce que le déploiement de l'agent touche hors du dossier du
// service : Compose et le disque.
type dockerTools struct {
	compose composeRunner
	// freeBytes rend l'espace libre sous un chemin, même s'il n'existe pas
	// encore.
	freeBytes func(path string) (uint64, error)
	// writeFiles écrit le contenu du dossier du service ; un test le remplace
	// pour faire échouer l'écriture.
	writeFiles func(dir *os.Root, job dockerJob) ([]agentwire.OutputLine, error)
}

func newDockerTools() dockerTools {
	return dockerTools{compose: execCompose{path: dockerBinary}, freeBytes: freeBytesUnder, writeFiles: writeServiceFiles}
}

// dockerJob est un déploiement jugé : tout ce que l'exécution lira de
// l'ordre. La racine vient du fichier de l'agent, jamais de l'ordre, qui ne
// porte que le nom du projet.
type dockerJob struct {
	params agentwire.DockerParams
	env    string
	hasEnv bool
	update bool
	root   string
}

// readDockerOrder juge l'ordre avant de l'accepter : un ordre mal formé est
// refusé tout de suite. Le secret, s'il y en a un, est le contenu de l'.env.
func readDockerOrder(body agentwire.OrderBody, root string, update bool) (dockerJob, *agentwire.ActionResult) {
	bad := &agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionFailed, Reason: agentwire.ReasonBadOrder}
	job := dockerJob{update: update, root: root}
	if err := json.Unmarshal(body.Params, &job.params); err != nil || !isDeployable(job.params) {
		return dockerJob{}, bad
	}
	if !job.params.EnvSent {
		if len(body.Secret) != 0 {
			return dockerJob{}, bad
		}
		return job, nil
	}
	var secret agentwire.DockerSecret
	if err := json.Unmarshal(body.Secret, &secret); err != nil || secret.Env == "" || !isEnvText(secret.Env) {
		return dockerJob{}, bad
	}
	job.env, job.hasEnv = secret.Env, true
	return job, nil
}

// isDeployable : le projet et les noms d'unité servent de noms de dossier,
// l'agent les rejuge ; le compose est du texte de la taille d'un ordre, et
// exactement celui que le serveur rend de l'annuaire (agentwire.CheckCompose) :
// un ordre signé ne fait pas monter autre chose que data/<unité>.
func isDeployable(params agentwire.DockerParams) bool {
	if !agentwire.IsComposeProject(params.Project) {
		return false
	}
	if params.Compose == "" || len(params.Compose) > agentwire.MaxComposeBytes || !isEnvText(params.Compose) {
		return false
	}
	if len(params.DataDirs) > agentwire.MaxDataDirs {
		return false
	}
	seen := map[string]bool{}
	for _, name := range params.DataDirs {
		if !agentwire.IsDataDirName(name) || seen[name] {
			return false
		}
		seen[name] = true
	}
	return agentwire.CheckCompose(params.Compose, params.Project, params.ServiceID, params.DataDirs) == nil
}

// isEnvText : du texte UTF-8 sans caractère nul.
func isEnvText(text string) bool {
	return utf8.ValidString(text) && !strings.ContainsRune(text, 0)
}

// deployService écrit le dossier du service sous la racine de l'agent, puis
// lance Compose. Dans l'ordre : la racine (rien n'y est à moi tant qu'elle
// n'est ni vide ni marquée), le dossier du service (il n'existe pas pour un
// déploiement, il existe pour un redéploiement), les prérequis, les écritures,
// Compose. Les refus d'avant les écritures ne laissent rien derrière eux ; un
// échec entre la création du dossier et Compose le retire.
func (t dockerTools) deployService(ctx context.Context, job dockerJob) ([]agentwire.OutputLine, error) {
	if !machine.ValidCapabilityPath(job.root) {
		return nil, failWith(agentwire.ReasonPathUnwritable, fmt.Errorf("services root %q is not a clean absolute path", job.root))
	}
	base, err := judgeRoot(job.root)
	if err != nil {
		return nil, err
	}
	if base != nil {
		defer base.Close()
	}
	if err := judgeServiceDir(base, job); err != nil {
		return nil, err
	}
	summary, err := t.checkPrerequisites(ctx, job.root)
	if err != nil {
		return summary, err
	}
	if base == nil {
		if base, err = createRoot(job.root); err != nil {
			return summary, err
		}
		defer base.Close()
	}
	return t.writeAndUp(ctx, base, job, summary)
}

// judgeRoot ouvre la racine : nil si elle n'existe pas encore (elle sera
// créée), refusée si elle existe, n'est pas vide et ne porte pas le témoin.
func judgeRoot(root string) (*os.Root, error) {
	base, err := os.OpenRoot(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, failWith(agentwire.ReasonPathUnwritable, err)
	}
	entries, err := fs.ReadDir(base.FS(), ".")
	if err != nil {
		_ = base.Close()
		return nil, failWith(agentwire.ReasonPathUnwritable, err)
	}
	if len(entries) > 0 && !hasWitness(base) {
		_ = base.Close()
		return nil, failWith(agentwire.ReasonRootNotOurs, fmt.Errorf("%s is not empty and holds no %s", root, brand.WorkspaceMarker))
	}
	return base, nil
}

// hasWitness : le fichier témoin que l'agent pose à la création de la racine.
func hasWitness(base *os.Root) bool {
	info, err := base.Lstat(brand.WorkspaceMarker)
	return err == nil && info.Mode().IsRegular()
}

// createRoot crée la racine, en 0755 : les dossiers dessous sont lisibles de
// tous, c'est l'.env qui est privé.
func createRoot(root string) (*os.Root, error) {
	if err := os.MkdirAll(root, serviceDirMode); err != nil { // #nosec G301 G304 G703 -- la racine est celle du fichier de l'agent (-root-dir, jugée par ValidCapabilityPath), jamais celle d'un ordre ; 0755 voulu : les dossiers des services sont lisibles.
		return nil, failWith(agentwire.ReasonPathUnwritable, err)
	}
	base, err := os.OpenRoot(root)
	if err != nil {
		return nil, failWith(agentwire.ReasonPathUnwritable, err)
	}
	return base, nil
}

// judgeServiceDir : un déploiement veut un dossier neuf, un redéploiement le
// dossier de son déploiement. Un lien symbolique n'est jamais suivi.
func judgeServiceDir(base *os.Root, job dockerJob) error {
	exists := false
	if base != nil {
		info, err := base.Lstat(job.params.Project)
		switch {
		case err == nil && info.Mode()&fs.ModeSymlink != 0:
			return failWith(agentwire.ReasonPathOutsideRight, fmt.Errorf("%s is a symbolic link", job.params.Project))
		case err == nil && !info.IsDir():
			return failWith(agentwire.ReasonPathUnwritable, fmt.Errorf("%s is not a directory", job.params.Project))
		case err == nil:
			exists = true
		case !errors.Is(err, fs.ErrNotExist):
			return failWith(agentwire.ReasonPathUnwritable, err)
		}
	}
	switch {
	case job.update && !exists:
		return failWith(agentwire.ReasonServiceDirMissing, fmt.Errorf("%s does not exist", job.params.Project))
	case !job.update && exists:
		return failWith(agentwire.ReasonServiceDirExists, fmt.Errorf("%s already exists", job.params.Project))
	case exists:
		return judgeDataDirs(base, job)
	}
	return nil
}

// judgeDataDirs lit, avant toute écriture, les dossiers de données d'un
// service déjà déployé : un lien symbolique à leur place refuse le
// redéploiement, plutôt que de le voir échouer à moitié.
func judgeDataDirs(base *os.Root, job dockerJob) error {
	if len(job.params.DataDirs) == 0 {
		return nil
	}
	dir, err := base.OpenRoot(job.params.Project)
	if err != nil {
		return failWith(agentwire.ReasonPathUnwritable, err)
	}
	defer dir.Close()
	for _, name := range append([]string{dataDirName}, prefixed(dataDirName, job.params.DataDirs)...) {
		info, err := dir.Lstat(name)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return failWith(agentwire.ReasonPathUnwritable, err)
		case info.Mode()&fs.ModeSymlink != 0:
			return failWith(agentwire.ReasonPathOutsideRight, fmt.Errorf("%s is a symbolic link", name))
		case !info.IsDir():
			return failWith(agentwire.ReasonPathUnwritable, fmt.Errorf("%s is not a directory", name))
		}
	}
	return nil
}

func prefixed(dir string, names []string) []string {
	joined := make([]string, 0, len(names))
	for _, name := range names {
		joined = append(joined, path.Join(dir, name))
	}
	return joined
}

// checkPrerequisites : Docker, une version de Compose qui sait ce que le
// fichier dit, de la place. Ni le processeur ni la mémoire ne se vérifient.
// Il rend la ligne de version, que le résultat garde.
func (t dockerTools) checkPrerequisites(ctx context.Context, root string) ([]agentwire.OutputLine, error) {
	versionCtx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	version, err := t.compose.Version(versionCtx)
	if errors.Is(err, errDockerMissing) {
		return nil, failWith(agentwire.ReasonDockerMissing, err)
	}
	if err != nil {
		// docker est là, mais pas son greffon compose (ou il ne répond pas).
		return nil, failWith(agentwire.ReasonComposeTooOld, err)
	}
	summary := []agentwire.OutputLine{stdout("compose " + version)}
	if !isComposeNewEnough(version) {
		return summary, failWith(agentwire.ReasonComposeTooOld, fmt.Errorf("compose %s is older than %d.%d.%d", version, minComposeVersion[0], minComposeVersion[1], minComposeVersion[2]))
	}
	free, err := t.freeBytes(root)
	if err != nil {
		return summary, failWith(agentwire.ReasonPathUnwritable, err)
	}
	if free < minFreeBytes {
		return summary, failWith(agentwire.ReasonDiskFull, fmt.Errorf("%d bytes free under %s", free, root))
	}
	return summary, nil
}

func isComposeNewEnough(version string) bool {
	match := composeVersionPattern.FindStringSubmatch(strings.TrimSpace(version))
	if match == nil {
		return false
	}
	for index, minimum := range minComposeVersion {
		number, err := strconv.Atoi(match[index+1])
		if err != nil {
			return false
		}
		if number != minimum {
			return number > minimum
		}
	}
	return true
}

// writeAndUp écrit le dossier du service et lance Compose. Le dossier d'un
// déploiement est créé ici ; s'il y a échec avant Compose, il est retiré, pour
// que le déploiement suivant ne se heurte pas à lui.
func (t dockerTools) writeAndUp(ctx context.Context, base *os.Root, job dockerJob, summary []agentwire.OutputLine) (output []agentwire.OutputLine, err error) {
	project := job.params.Project
	if err := ensureWitness(base); err != nil {
		return summary, err
	}
	if !job.update {
		if err := base.Mkdir(project, serviceDirMode); err != nil {
			return summary, failWith(agentwire.ReasonPathUnwritable, err)
		}
		defer func() {
			if err != nil && !isComposeFailure(err) {
				_ = base.RemoveAll(project)
			}
		}()
		if err := base.Chmod(project, serviceDirMode); err != nil {
			return summary, failWith(agentwire.ReasonPathUnwritable, err)
		}
	}
	dir, err := base.OpenRoot(project)
	if err != nil {
		return summary, failWith(agentwire.ReasonPathUnwritable, err)
	}
	defer dir.Close()
	written, err := t.writeFiles(dir, job)
	summary = append(summary, written...)
	if err != nil {
		return summary, err
	}
	lines, upErr := t.compose.Up(ctx, path.Join(job.root, project), project)
	// Compose cite parfois une ligne de l'.env (une erreur de syntaxe) : ses
	// valeurs sont masquées avant de partir au serveur.
	lines = redactLines(lines, envSecrets(envOf(dir, job)))
	output = append(summary, lines[max(len(lines)-(agentwire.MaxOutputLines-len(summary)), 0):]...)
	if upErr != nil {
		return output, failWith(agentwire.ReasonComposeFailed, upErr)
	}
	return output, nil
}

func isComposeFailure(err error) bool {
	var failed *orderError
	return errors.As(err, &failed) && failed.reason == agentwire.ReasonComposeFailed
}

// ensureWitness pose le fichier témoin si la racine n'en a pas : une racine
// vide ou neuve devient la nôtre.
func ensureWitness(base *os.Root) error {
	if hasWitness(base) {
		return nil
	}
	if err := fsx.WriteAtomic(base, brand.WorkspaceMarker, nil, composeFileMode); err != nil {
		return failWith(agentwire.ReasonPathUnwritable, err)
	}
	return nil
}

// writeServiceFiles écrit, dans le dossier du service, le compose.yml (0644),
// l'.env s'il est venu (0600) et les dossiers de données (0755). Chaque
// fichier est écrit à côté puis renommé : un lecteur voit l'ancien ou le
// nouveau, jamais la moitié.
func writeServiceFiles(dir *os.Root, job dockerJob) ([]agentwire.OutputLine, error) {
	var written []agentwire.OutputLine
	if err := fsx.WriteAtomic(dir, composeFileName, []byte(job.params.Compose), composeFileMode); err != nil {
		return written, failWith(agentwire.ReasonPathUnwritable, err)
	}
	written = append(written, stdout("written "+composeFileName))
	if job.hasEnv {
		if err := fsx.WriteAtomic(dir, envFileName, []byte(job.env), envFileMode); err != nil {
			return written, failWith(agentwire.ReasonPathUnwritable, err)
		}
		written = append(written, stdout("written "+envFileName))
	}
	if len(job.params.DataDirs) == 0 {
		return written, nil
	}
	if err := ensureDir(dir, dataDirName); err != nil {
		return written, err
	}
	for _, name := range prefixed(dataDirName, job.params.DataDirs) {
		if err := ensureDir(dir, name); err != nil {
			return written, err
		}
	}
	return append(written, stdout("data "+strings.Join(job.params.DataDirs, " "))), nil
}

// ensureDir crée un dossier en 0755 s'il manque. Un dossier qui existe garde
// son mode et son propriétaire (l'UID du conteneur le règle à la main) ; un
// lien symbolique ou un fichier à sa place est refusé.
func ensureDir(dir *os.Root, name string) error {
	info, err := dir.Lstat(name)
	switch {
	case err == nil && info.Mode()&fs.ModeSymlink != 0:
		return failWith(agentwire.ReasonPathOutsideRight, fmt.Errorf("%s is a symbolic link", name))
	case err == nil && info.IsDir():
		return nil
	case err == nil:
		return failWith(agentwire.ReasonPathUnwritable, fmt.Errorf("%s is not a directory", name))
	case !errors.Is(err, fs.ErrNotExist):
		return failWith(agentwire.ReasonPathUnwritable, err)
	}
	if err := dir.Mkdir(name, serviceDirMode); err != nil {
		return failWith(agentwire.ReasonPathUnwritable, err)
	}
	if err := dir.Chmod(name, serviceDirMode); err != nil {
		return failWith(agentwire.ReasonPathUnwritable, err)
	}
	return nil
}

// Le masquage de l'.env dans la sortie de Compose. Une valeur plus courte que
// minSecretBytes ne se masque pas : « true » ou « 8080 » effaceraient la
// moitié de la sortie, et ne sont pas des secrets.
const (
	minSecretBytes = 6
	redactedText   = "[.env]"
	// Un .env relu sur la machine pour le masquage : la taille d'un secret
	// d'ordre, avec de la marge.
	maxEnvReadBytes = 2 * agentwire.MaxSecretBytes
)

// envOf rend l'.env que Compose lit : celui de l'ordre, ou celui que la
// machine garde d'un déploiement précédent. Vide s'il n'y en a pas, ou s'il
// ne se lit pas comme un fichier ordinaire de taille raisonnable.
func envOf(dir *os.Root, job dockerJob) string {
	if job.hasEnv {
		return job.env
	}
	info, err := dir.Lstat(envFileName)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxEnvReadBytes {
		return ""
	}
	content, err := dir.ReadFile(envFileName)
	if err != nil {
		return ""
	}
	return string(content)
}

// envSecrets rend les valeurs d'un .env, les plus longues d'abord : une ligne
// sans « = » compte en entier (Compose la citerait dans son erreur).
func envSecrets(env string) []string {
	var secrets []string
	for line := range strings.Lines(env) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		value := line
		if _, after, found := strings.Cut(line, "="); found {
			value = strings.TrimSpace(after)
		}
		for _, candidate := range []string{value, strings.Trim(value, `"'`)} {
			if len(candidate) >= minSecretBytes && !slices.Contains(secrets, candidate) {
				secrets = append(secrets, candidate)
			}
		}
	}
	slices.SortFunc(secrets, func(a, b string) int { return len(b) - len(a) })
	return secrets
}

// redactLines remplace chaque valeur de l'.env dans la sortie.
func redactLines(lines []agentwire.OutputLine, secrets []string) []agentwire.OutputLine {
	if len(secrets) == 0 {
		return lines
	}
	redacted := make([]agentwire.OutputLine, 0, len(lines))
	for _, line := range lines {
		for _, secret := range secrets {
			line.Line = strings.ReplaceAll(line.Line, secret, redactedText)
		}
		redacted = append(redacted, line)
	}
	return redacted
}

// dockerResultOf rend ce que le serveur lira d'un déploiement fini. Ce que
// l'agent a refusé avant d'écrire est « refusé » (rien n'a changé) ; Compose
// qui échoue, ou une écriture qui manque, est « échoué ».
func dockerResultOf(runID string, output []agentwire.OutputLine, err error) agentwire.ActionResult {
	result := resultOf(runID, output, err)
	if result.State == agentwire.ActionFailed && isRefusedBeforeWriting(result.Reason) {
		result.State = agentwire.ActionRefused
	}
	return result
}

func isRefusedBeforeWriting(reason string) bool {
	switch reason {
	case agentwire.ReasonServiceDirExists, agentwire.ReasonServiceDirMissing, agentwire.ReasonRootNotOurs,
		agentwire.ReasonDockerMissing, agentwire.ReasonComposeTooOld, agentwire.ReasonDiskFull,
		agentwire.ReasonPathOutsideRight:
		return true
	}
	return false
}

// execCompose lance le vrai Docker.
type execCompose struct {
	path string
}

func (c execCompose) Version(ctx context.Context) (string, error) {
	// bounded: le programme est une constante de l'agent, les arguments sont fixes.
	command := exec.CommandContext(ctx, c.path, "compose", "version", "--short") // #nosec G204 -- bounded: chemin constant, arguments constants.
	command.Env = composeEnvironment
	var out bytes.Buffer
	command.Stdout = &out
	if err := command.Run(); err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
			return "", errDockerMissing
		}
		return "", fmt.Errorf("docker compose version: %w", err)
	}
	return strings.TrimSpace(out.String()), nil
}

func (c execCompose) Up(ctx context.Context, dir, project string) ([]agentwire.OutputLine, error) {
	ctx, cancel := context.WithTimeout(ctx, composeUpTimeout)
	defer cancel()
	// bounded: le programme est une constante de l'agent ; dir est
	// <racine du fichier de l'agent>/<projet>, le projet rejugé par
	// IsComposeProject (il ne commence jamais par « - »). -p fixe le nom du
	// projet : un COMPOSE_PROJECT_NAME de l'.env ne fait pas viser (ni
	// --remove-orphans nettoyer) un autre projet. L'.env et le compose sont
	// des fichiers, jamais des arguments.
	command := exec.CommandContext(ctx, c.path, "compose", "--project-directory", dir, // #nosec G204 -- bounded: chemin constant, dir = racine de l'agent + projet jugé.
		"-f", path.Join(dir, composeFileName), "-p", project, "up", "-d", "--remove-orphans", "--pull", "missing")
	command.Env = composeEnvironment
	command.WaitDelay = 5 * time.Second
	tail := newLineTail(composeTailLines)
	command.Stdout = tail.writer("stdout")
	command.Stderr = tail.writer("stderr")
	err := command.Run()
	tail.flush()
	if err != nil && ctx.Err() != nil {
		err = fmt.Errorf("docker compose up: %w", ctx.Err())
	}
	return tail.lines(), err
}

// lineTail garde les dernières lignes de deux flux, dans l'ordre où elles
// arrivent : la fin d'une sortie de Compose dit pourquoi il a échoué.
type lineTail struct {
	mu    sync.Mutex
	max   int
	kept  []agentwire.OutputLine
	parts []*tailWriter
}

func newLineTail(max int) *lineTail {
	return &lineTail{max: max}
}

func (t *lineTail) writer(stream string) *tailWriter {
	w := &tailWriter{tail: t, stream: stream}
	t.parts = append(t.parts, w)
	return w
}

// flush pousse les lignes que le dernier octet n'a pas terminées.
func (t *lineTail) flush() {
	for _, w := range t.parts {
		w.flush()
	}
}

func (t *lineTail) lines() []agentwire.OutputLine {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]agentwire.OutputLine(nil), t.kept...)
}

func (t *lineTail) push(stream, line string) {
	line = cleanLine(line)
	if line == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.kept = append(t.kept, agentwire.OutputLine{Stream: stream, Line: line})
	if len(t.kept) > t.max {
		t.kept = append(t.kept[:0], t.kept[len(t.kept)-t.max:]...)
	}
}

// tailWriter découpe un flux en lignes (le retour chariot des barres de
// progression en est un aussi) ; une ligne trop longue part en morceaux.
type tailWriter struct {
	tail    *lineTail
	stream  string
	partial []byte
}

func (w *tailWriter) Write(data []byte) (int, error) {
	for _, b := range data {
		if b == '\n' || b == '\r' {
			w.flush()
			continue
		}
		w.partial = append(w.partial, b)
		if len(w.partial) >= agentwire.MaxOutputLineBytes {
			w.flush()
		}
	}
	return len(data), nil
}

func (w *tailWriter) flush() {
	if len(w.partial) == 0 {
		return
	}
	w.tail.push(w.stream, string(w.partial))
	w.partial = w.partial[:0]
}

// cleanLine rend une ligne que le serveur acceptera : de l'UTF-8 valide, sans
// caractère de contrôle, d'au plus MaxOutputLineBytes octets.
func cleanLine(line string) string {
	line = strings.ToValidUTF8(line, "�")
	line = strings.Map(func(r rune) rune {
		if r < ' ' && r != '\t' || r == 0x7f {
			return ' '
		}
		return r
	}, line)
	line = strings.TrimSpace(line)
	if len(line) <= agentwire.MaxOutputLineBytes {
		return line
	}
	// Couper entre deux octets d'un même caractère rendrait la ligne invalide.
	cut := agentwire.MaxOutputLineBytes
	for cut > 0 && !utf8.RuneStart(line[cut]) {
		cut--
	}
	return line[:cut]
}

// freeBytesUnder rend l'espace que root peut encore écrire sous ce chemin, ou
// sous son plus proche dossier existant s'il n'existe pas encore.
func freeBytesUnder(target string) (uint64, error) {
	for {
		var stat syscall.Statfs_t
		err := syscall.Statfs(target, &stat)
		if err == nil {
			blockSize := uint64(stat.Bsize) // #nosec G115 -- une taille de bloc, toujours positive.
			return stat.Bavail * blockSize, nil
		}
		if !errors.Is(err, syscall.ENOENT) || target == "/" {
			return 0, err
		}
		target = path.Dir(target)
	}
}
