package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/fsx"
	"github.com/ldesfontaine/opencloud/internal/restic"
)

// La cible restic (droit restic.target) : un rest-server sur le dossier des
// dépôts, en écriture seule, un utilisateur par source (htpasswd,
// --private-repos), en TLS. Les sources y écrivent en direct ; l'exécuteur du
// relais s'y présente aussi, par la boucle locale, sous l'utilisateur du
// dépôt que le serveur a nommé, avec le mot de passe REST que la cible garde.
// Aucune instance sans authentification.
const (
	targetConfigFile   = "target.json"
	retentionFloorFile = "retention-floor.json"
	htpasswdFile       = "htpasswd"
	// Le mot de passe REST de chaque source, un fichier par utilisateur :
	// l'exécuteur du relais le présente à rest-server.
	relayPasswordsDir = "relay-passwords"
	maxTargetConfig   = 4 << 10
	loopbackHost      = "127.0.0.1"
	publicProcessName = "rest-server"
	// La taille des dépôts se compte au plus ce temps : au-delà, la somme
	// rapportée est celle déjà comptée (un minimum).
	repoSizeTimeout = 10 * time.Second
)

// targetConfig est ce que restic.target.setup a posé, gardé pour relancer
// rest-server au démarrage de l'agent.
type targetConfig struct {
	Listen string   `json:"listen"`
	Port   int      `json:"port"`
	Hosts  []string `json:"hosts"`
}

// resticTarget tient le rest-server de la cible et ce qui s'y rapporte : le
// relais, la rétention.
type resticTarget struct {
	stateDir *os.Root
	logger   *slog.Logger
	changed  func()
	now      func() time.Time
	// tuneSupervisor règle un superviseur neuf ; les tests y raccourcissent
	// les délais et s'y abonnent aux lancements.
	tuneSupervisor func(*processSupervisor)
	// runner lance restic forget pour restic.retain ; les tests en donnent un
	// qui lance un faux binaire.
	runner resticRunner
	// Les requêtes relayées en cours.
	relaySlots chan struct{}

	mu         sync.Mutex
	ctx        context.Context
	stop       context.CancelFunc
	done       chan struct{}
	supervisor *processSupervisor
	// relay : l'adresse de rest-server vue de la cible et le client qui
	// n'épingle que son certificat ; posés au lancement.
	relay *relayEndpoint
	// failure : ce qui empêche de lancer rest-server avant même d'essayer.
	failure string
	// Les verrous qu'un restic relayé a posés, par dépôt, et les dépôts en
	// cours de rétention : une rétention ne part pas sur un dépôt occupé.
	relayLocks map[string]map[string]time.Time
	retaining  map[string]bool
}

// relayEndpoint est ce que l'exécuteur du relais appelle.
type relayEndpoint struct {
	address *url.URL
	client  *http.Client
}

func newResticTarget(stateDir *os.Root, logger *slog.Logger, changed func()) *resticTarget {
	return &resticTarget{
		stateDir: stateDir, logger: logger, changed: changed, now: time.Now, tuneSupervisor: func(*processSupervisor) {},
		runner: execResticRunner{}, relaySlots: make(chan struct{}, maxRelayInFlight),
		relayLocks: map[string]map[string]time.Time{}, retaining: map[string]bool{},
	}
}

// start donne le contexte de l'agent, sous lequel rest-server vivra, et le
// relance si une configuration existe. dir est le chemin du droit.
func (t *resticTarget) start(ctx context.Context, dir string, checks hostChecks) {
	t.mu.Lock()
	t.ctx = ctx
	t.mu.Unlock()
	config, err := t.loadConfig()
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		t.logger.Warn("restic target config unreadable", "error", err)
		return
	}
	// Un certificat tiré avant le relais authentifié n'a pas l'adresse de la
	// boucle locale : il se tire à neuf ici, et le rapport porte le nouveau.
	if err := t.renewCertificate(config); err != nil {
		t.logger.Warn("restic target certificate not renewed", "error", err)
	}
	if reason := t.launch(config, dir, checks); reason != "" {
		t.logger.Warn("restic target not started", "reason", reason)
	}
}

// setup fait restic.target.setup : le plancher de rétention, le certificat,
// le fichier htpasswd, la configuration, puis (re)lance rest-server. Rien
// n'attend qu'il réponde : le rapport dit son état. Un plancher plus bas que
// celui posé est refusé avant toute écriture.
func (t *resticTarget) setup(params agentwire.ResticTargetSetupParams, dir string, checks hostChecks) ([]agentwire.OutputLine, error) {
	if reason := checks.writable(dir); reason != "" {
		return nil, refuseWith(reason, errors.New("the repositories folder is not writable"))
	}
	resticRoot, err := openResticDir(t.stateDir)
	if err != nil {
		return nil, failWith(agentwire.ReasonPathUnwritable, err)
	}
	defer resticRoot.Close()
	floor, err := nextRetentionFloor(resticRoot, params.RetentionFloor)
	if err != nil {
		return nil, err
	}
	config := targetConfig{Listen: params.Listen, Port: params.Port, Hosts: params.Hosts}
	certificatePEM, err := ensureTargetCertificate(resticRoot, certificateNames(config), t.now())
	if err != nil {
		return nil, failWith(agentwire.ReasonPathUnwritable, err)
	}
	if err := ensureHtpasswd(resticRoot); err != nil {
		return nil, failWith(agentwire.ReasonPathUnwritable, err)
	}
	if err := saveJSON(resticRoot, retentionFloorFile, floor); err != nil {
		return nil, failWith(agentwire.ReasonPathUnwritable, err)
	}
	if err := saveJSON(resticRoot, targetConfigFile, config); err != nil {
		return nil, failWith(agentwire.ReasonPathUnwritable, err)
	}
	output := []agentwire.OutputLine{
		resticOutput("certificate sha256 " + fingerprintOf(certificatePEM)),
		resticOutput("listening " + net.JoinHostPort(config.Listen, strconv.Itoa(config.Port))),
		resticOutput(fmt.Sprintf("retention floor daily %d weekly %d monthly %d", floor.KeepDaily, floor.KeepWeekly, floor.KeepMonthly)),
	}
	// Le rapport de la cible dit la raison précise ; l'ordre, la plus proche.
	switch reason := t.launch(config, dir, checks); reason {
	case "":
		return output, nil
	case agentwire.ResticErrorBinaryMissing:
		return output, failWith(agentwire.ReasonRestServerMissing, errors.New("rest-server not found"))
	default:
		return output, failWith(agentwire.ReasonPathUnwritable, errors.New("rest-server cannot start: "+reason))
	}
}

func (t *resticTarget) renewCertificate(config targetConfig) error {
	resticRoot, err := openResticDir(t.stateDir)
	if err != nil {
		return err
	}
	defer resticRoot.Close()
	_, err = ensureTargetCertificate(resticRoot, certificateNames(config), t.now())
	return err
}

// relayHost est l'adresse par laquelle l'exécuteur du relais joint
// rest-server sur la machine même : la boucle locale quand il écoute toutes
// les adresses, sinon l'adresse d'écoute.
func relayHost(listen string) string {
	ip := net.ParseIP(listen)
	if ip == nil || ip.IsUnspecified() {
		return loopbackHost
	}
	return ip.String()
}

// certificateNames : les adresses annoncées aux sources, puis celle du
// relais, que l'exécuteur vérifie comme une source.
func certificateNames(config targetConfig) []string {
	return append(slices.Clone(config.Hosts), relayHost(config.Listen))
}

func (t *resticTarget) loadConfig() (targetConfig, error) {
	resticRoot, err := openResticDir(t.stateDir)
	if err != nil {
		return targetConfig{}, err
	}
	defer resticRoot.Close()
	content, err := fsx.ReadPrivate(resticRoot, targetConfigFile, maxTargetConfig)
	if err != nil {
		return targetConfig{}, err
	}
	var config targetConfig
	if err := json.Unmarshal(content, &config); err != nil {
		return targetConfig{}, fmt.Errorf("parse target config: %w", err)
	}
	return config, nil
}

// saveJSON écrit un petit fichier d'état d'un seul coup.
func saveJSON(resticRoot *os.Root, name string, value any) error {
	content, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s: %w", name, err)
	}
	return fsx.WriteAtomic(resticRoot, name, content, resticFileMode)
}

// ensureHtpasswd pose un fichier vide s'il manque : rest-server refuse de
// démarrer sans lui.
func ensureHtpasswd(resticRoot *os.Root) error {
	err := fsx.CreateExclusive(resticRoot, htpasswdFile, nil, resticFileMode)
	if err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("create htpasswd: %w", err)
	}
	return nil
}

// launch arrête le rest-server en place et lance celui de la configuration.
// Il rend la raison qui empêche de le lancer, vide sinon.
func (t *resticTarget) launch(config targetConfig, dir string, checks hostChecks) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopLocked()
	reason := t.prerequisiteReason(dir, checks)
	t.failure = reason
	if reason != "" || t.ctx == nil {
		t.changed()
		return reason
	}
	binary, _ := checks.findRestServer()
	files, err := t.targetFiles()
	if err != nil {
		t.failure = agentwire.ResticErrorCertificate
		t.changed()
		return t.failure
	}
	relay, err := t.newRelayEndpoint(config)
	if err != nil {
		t.failure = agentwire.ResticErrorCertificate
		t.changed()
		return t.failure
	}
	ctx, cancel := context.WithCancel(t.ctx)
	t.stop, t.relay = cancel, relay
	t.supervisor = newProcessSupervisor(publicProcessName, publicLaunch(binary, dir, config, files), t.logger, t.changed)
	t.tuneSupervisor(t.supervisor)
	done := make(chan struct{})
	t.done = done
	supervisor := t.supervisor
	go func() {
		supervisor.run(ctx)
		close(done)
	}()
	t.changed()
	return ""
}

// newRelayEndpoint rend l'adresse de rest-server vue de la cible et un
// client qui n'admet que son certificat, en TLS 1.3 : l'exécuteur le
// vérifie comme le ferait une source, et ne passe par aucun mandataire.
func (t *resticTarget) newRelayEndpoint(config targetConfig) (*relayEndpoint, error) {
	certificatePEM, _ := t.publicCertificate()
	certificate, err := agentwire.ParsePublicCertificate(certificatePEM)
	if err != nil {
		return nil, err
	}
	pinned := x509.NewCertPool()
	pinned.AddCert(certificate)
	transport := &http.Transport{
		TLSClientConfig:     &tls.Config{RootCAs: pinned, MinVersion: tls.VersionTLS13},
		MaxIdleConnsPerHost: maxRelayInFlight,
	}
	address := &url.URL{Scheme: "https", Host: net.JoinHostPort(relayHost(config.Listen), strconv.Itoa(config.Port))}
	return &relayEndpoint{address: address, client: &http.Client{Transport: transport}}, nil
}

// prerequisiteReason : ce qui doit tenir avant de lancer rest-server, dans
// l'ordre où l'opérateur le règle.
func (t *resticTarget) prerequisiteReason(dir string, checks hostChecks) string {
	if dir == "" || checks.writable(dir) != "" {
		return agentwire.ResticErrorDirUnwritable
	}
	if _, err := checks.findRestServer(); err != nil {
		return agentwire.ResticErrorBinaryMissing
	}
	resticRoot, err := openResticDir(t.stateDir)
	if err != nil {
		return agentwire.ResticErrorCertificate
	}
	defer resticRoot.Close()
	if _, err := restic.ReadHtpasswd(resticRoot, htpasswdFile); err != nil {
		return agentwire.ResticErrorHtpasswdInvalid
	}
	if err := os.MkdirAll(dir, resticDirMode); err != nil { // #nosec G301 G703 -- le chemin du droit restic.target, lu dans le fichier de l'agent.
		return agentwire.ResticErrorDirUnwritable
	}
	return ""
}

// stopLocked arrête rest-server et attend qu'il soit sorti : deux processus
// ne se disputent jamais le même port.
func (t *resticTarget) stopLocked() {
	if t.stop == nil {
		return
	}
	t.stop()
	<-t.done
	if t.relay != nil {
		t.relay.client.CloseIdleConnections()
	}
	t.stop, t.done, t.supervisor, t.relay = nil, nil, nil, nil
}

// targetFiles rend les chemins absolus que rest-server lit.
type targetFiles struct {
	certificate, key, htpasswd string
}

func (t *resticTarget) targetFiles() (targetFiles, error) {
	base, err := absoluteStatePath(t.stateDir, resticDir)
	if err != nil {
		return targetFiles{}, err
	}
	return targetFiles{
		certificate: filepath.Join(base, targetCertificateFile),
		key:         filepath.Join(base, targetKeyFile),
		htpasswd:    filepath.Join(base, htpasswdFile),
	}, nil
}

// publicLaunch : l'écoute annoncée, en TLS 1.3, un utilisateur par source.
func publicLaunch(binary, dir string, config targetConfig, files targetFiles) func(ctx context.Context) *exec.Cmd {
	listen := net.JoinHostPort(config.Listen, strconv.Itoa(config.Port))
	return func(ctx context.Context) *exec.Cmd {
		// bounded: le binaire vient de findBinary, dir du droit restic.target,
		// listen d'un ordre jugé (IP, port), les fichiers de l'état de l'agent.
		command := exec.CommandContext(ctx, binary, "--path", dir, "--listen", listen, // #nosec G204 -- bounded: voir ci-dessus.
			"--tls", "--tls-cert", files.certificate, "--tls-key", files.key, "--tls-min-ver", "1.3",
			"--append-only", "--private-repos", "--htpasswd-file", files.htpasswd)
		command.Env = producerEnvironment
		return command
	}
}

// setUser ajoute ou retire la ligne d'une source, puis fait relire le
// fichier à rest-server (SIGHUP). password vide : retirer. La cible calcule
// l'empreinte bcrypt du mot de passe REST reçu, et garde ce mot de passe
// (0600) pour l'exécuteur du relais ; une ligne déjà juste ne change pas.
func (t *resticTarget) setUser(user, password string) ([]agentwire.OutputLine, error) {
	resticRoot, err := openResticDir(t.stateDir)
	if err != nil {
		return nil, failWith(agentwire.ReasonPathUnwritable, err)
	}
	defer resticRoot.Close()
	entries, err := restic.ReadHtpasswd(resticRoot, htpasswdFile)
	if err != nil {
		return nil, failWith(agentwire.ReasonPathUnwritable, err)
	}
	line := "user added " + user
	if password == "" {
		entries = restic.RemoveHtpasswdUser(entries, user)
		line = "user removed " + user
		if err := removeRelayPassword(resticRoot, user); err != nil {
			return nil, failWith(agentwire.ReasonPathUnwritable, err)
		}
	} else {
		if err := writeRelayPassword(resticRoot, user, password); err != nil {
			return nil, failWith(agentwire.ReasonPathUnwritable, err)
		}
		if entries, err = withUserPassword(entries, user, password); err != nil {
			return nil, failWith(agentwire.ReasonPathUnwritable, err)
		}
	}
	if err := restic.WriteHtpasswd(resticRoot, htpasswdFile, entries); err != nil {
		return nil, failWith(agentwire.ReasonPathUnwritable, err)
	}
	t.reload()
	t.changed()
	return []agentwire.OutputLine{resticOutput(line)}, nil
}

// withUserPassword pose la ligne de l'utilisateur ; une empreinte qui
// vérifie déjà ce mot de passe est gardée telle quelle.
func withUserPassword(entries []restic.HtpasswdEntry, user, password string) ([]restic.HtpasswdEntry, error) {
	for _, entry := range entries {
		if entry.User == user && restic.PasswordMatches(entry.Hash, []byte(password)) {
			return entries, nil
		}
	}
	hash, err := restic.HashPassword([]byte(password))
	if err != nil {
		return nil, err
	}
	return restic.SetHtpasswdUser(entries, restic.HtpasswdEntry{User: user, Hash: hash})
}

func writeRelayPassword(resticRoot *os.Root, user, password string) error {
	if err := resticRoot.MkdirAll(relayPasswordsDir, resticDirMode); err != nil {
		return fmt.Errorf("create relay passwords dir: %w", err)
	}
	if err := fsx.WriteAtomic(resticRoot, path.Join(relayPasswordsDir, user), []byte(password), resticFileMode); err != nil {
		return fmt.Errorf("write relay password: %w", err)
	}
	return nil
}

func removeRelayPassword(resticRoot *os.Root, user string) error {
	err := resticRoot.Remove(path.Join(relayPasswordsDir, user))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove relay password: %w", err)
	}
	return nil
}

// relayPassword rend le mot de passe REST d'un dépôt pour l'exécuteur du
// relais ; faux sans restic.user.add, ou s'il n'a pas la forme attendue.
func (t *resticTarget) relayPassword(repo string) (string, bool) {
	if !agentwire.IsResticUser(repo) {
		return "", false
	}
	resticRoot, err := openResticDir(t.stateDir)
	if err != nil {
		return "", false
	}
	defer resticRoot.Close()
	content, err := fsx.ReadPrivate(resticRoot, path.Join(relayPasswordsDir, repo), maxSecretFileBytes)
	if err != nil || !agentwire.IsResticPassword(string(content)) {
		return "", false
	}
	return string(content), true
}

// reload fait relire le fichier htpasswd à rest-server.
func (t *resticTarget) reload() {
	t.mu.Lock()
	supervisor := t.supervisor
	t.mu.Unlock()
	if supervisor == nil {
		return
	}
	if err := supervisor.reload(); err != nil {
		t.logger.Warn("reload rest-server", "error", err)
	}
}

// relayTarget rend ce que l'exécuteur du relais appelle, faux sans cible
// lancée.
func (t *resticTarget) relayTarget() (*relayEndpoint, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.relay, t.relay != nil
}

// report rend la section cible du signal : l'état de rest-server, ce que les
// sources épinglent, le plancher de rétention. Jamais un mot de passe, une
// empreinte ni la clé.
func (t *resticTarget) report(ctx context.Context, dir string, checks hostChecks) agentwire.ResticTargetReport {
	report := agentwire.ResticTargetReport{State: agentwire.ResticTargetStopped}
	t.mu.Lock()
	failure, supervisor := t.failure, t.supervisor
	t.mu.Unlock()
	switch {
	case failure != "":
		report.State, report.Reason = agentwire.ResticTargetError, failure
	case supervisor != nil:
		report.State, report.Reason = supervisor.status()
	}
	if binary, err := checks.findRestServer(); err == nil {
		report.Version = readBinaryVersion(ctx, binary, "--version")
	}
	if config, err := t.loadConfig(); err == nil && len(config.Hosts) > 0 {
		report.Host, report.Port = config.Hosts[0], config.Port
	}
	report.Certificate, report.Fingerprint = t.publicCertificate()
	report.Users = t.users()
	if floor, err := t.readRetentionFloor(); err == nil {
		report.RetentionFloor = &floor
	}
	if dir != "" {
		report.RepoBytes = repositoryBytes(ctx, dir)
		report.DiskTotal, report.DiskFree = diskOf(dir)
	}
	return report
}

// publicCertificate relit le certificat public ; rien s'il ne se lit pas
// comme un seul certificat.
func (t *resticTarget) publicCertificate() (string, string) {
	resticRoot, err := openResticDir(t.stateDir)
	if err != nil {
		return "", ""
	}
	defer resticRoot.Close()
	content, err := fsx.ReadPrivate(resticRoot, targetCertificateFile, maxCertificateFile)
	if err != nil {
		return "", ""
	}
	certificate, err := agentwire.ParsePublicCertificate(string(content))
	if err != nil {
		return "", ""
	}
	return string(content), agentwire.CertificateFingerprint(certificate.Raw)
}

func fingerprintOf(certificatePEM string) string {
	certificate, err := agentwire.ParsePublicCertificate(certificatePEM)
	if err != nil {
		return ""
	}
	return agentwire.CertificateFingerprint(certificate.Raw)
}

// users rend les noms du fichier htpasswd, sans leurs empreintes.
func (t *resticTarget) users() []string {
	resticRoot, err := openResticDir(t.stateDir)
	if err != nil {
		return nil
	}
	defer resticRoot.Close()
	entries, err := restic.ReadHtpasswd(resticRoot, htpasswdFile)
	if err != nil {
		return nil
	}
	users := make([]string, 0, len(entries))
	for _, entry := range entries {
		users = append(users, entry.User)
	}
	return users
}

// repositoryBytes additionne la taille des fichiers sous le dossier des
// dépôts, sans suivre de lien, et s'arrête au délai : la somme est alors un
// minimum. Un dossier de plusieurs To se parcourt sans le lire.
func repositoryBytes(ctx context.Context, dir string) int64 {
	ctx, cancel := context.WithTimeout(ctx, repoSizeTimeout)
	defer cancel()
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return filepath.SkipAll
		}
		if err != nil || !entry.Type().IsRegular() {
			return nil
		}
		if info, err := entry.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// diskOf rend la taille et le libre (pour un compte sans privilège) du
// disque du dossier ; zéro s'il ne se lit pas.
func diskOf(dir string) (int64, int64) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		return 0, 0
	}
	blockSize := int64(stat.Bsize)          // #nosec G115 -- une taille de bloc, toujours positive.
	total := int64(stat.Blocks) * blockSize // #nosec G115 -- un nombre de blocs d'un disque réel tient dans un int64.
	free := int64(stat.Bavail) * blockSize  // #nosec G115 -- idem.
	return total, free
}
