package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/restic"
)

const (
	// Toute la sauvegarde, rejeu compris, tient sous MaxRunDuration (une
	// heure, côté serveur), comme une production.
	resticBackupTimeout = 55 * time.Minute
	// Un échec se rejoue une fois (limite n° 2 : en écriture seule, un envoi
	// dont la réponse s'est perdue fait échouer la passe, la suivante passe).
	resticAttempts = 2
	// La fin de la sortie d'erreur de restic, filtrée, part dans le résultat.
	resticTailLines = 32
	// restic sort en 3 quand l'instantané est fait mais que des fichiers
	// n'ont pas pu être lus : la sauvegarde existe, incomplète.
	resticExitIncomplete = 3
	redactedSecret       = "[secret]"
)

// resticBackupJob est un ordre restic.backup jugé : tout ce que l'exécution
// lira de lui.
type resticBackupJob struct {
	params agentwire.ResticBackupParams
	binary string
	// directory : le dossier du service, sous la racine de l'agent (portée
	// service) ; vide pour un invité.
	directory string
}

// readResticBackup juge l'ordre avant de l'accepter : sa forme, le droit
// backup.produce pour un invité, restic présent, et pour un service son
// dossier sous la racine. Un refus ne lance rien.
func readResticBackup(body agentwire.OrderBody, capabilities []machine.Capability, root string, checks hostChecks) (resticBackupJob, *agentwire.ActionResult) {
	refused := func(reason string) *agentwire.ActionResult {
		return &agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionRefused, Reason: reason}
	}
	var params agentwire.ResticBackupParams
	if err := json.Unmarshal(body.Params, &params); err != nil || params.Validate() != nil {
		return resticBackupJob{}, &agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionFailed, Reason: agentwire.ReasonBadOrder}
	}
	if params.Scope == agentwire.ResticScopeVM && !machine.CoversProducer(capabilities, machine.ProducerProxmoxVzdump) {
		return resticBackupJob{}, refused(agentwire.RightMissing(machine.CapabilityBackupProduce))
	}
	binary, err := checks.findRestic()
	if err != nil {
		return resticBackupJob{}, refused(agentwire.ReasonResticMissing)
	}
	job := resticBackupJob{params: params, binary: binary}
	if params.Scope == agentwire.ResticScopeService {
		directory, reason := serviceDirectory(root, params.Service)
		if reason != "" {
			return resticBackupJob{}, refused(reason)
		}
		job.directory = directory
	}
	return job, nil
}

// serviceDirectory rend le dossier du service s'il existe et reste sous la
// racine, liens résolus : un lien vers /etc ne fait pas sauvegarder /etc.
// Les volumes nommés de Docker ne sont pas sous la racine : non couverts.
func serviceDirectory(root, service string) (string, string) {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", agentwire.ReasonServiceDirMissing
	}
	candidate := filepath.Join(root, service)
	info, err := os.Lstat(candidate)
	if errors.Is(err, fs.ErrNotExist) {
		return "", agentwire.ReasonServiceDirMissing
	}
	if err != nil {
		return "", agentwire.ReasonPathUnwritable
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return "", agentwire.ReasonPathOutsideRight
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil || !strings.HasPrefix(resolved, resolvedRoot+string(filepath.Separator)) {
		return "", agentwire.ReasonPathOutsideRight
	}
	return resolved, ""
}

// linkFiles sont les chemins absolus d'un lien que restic lit lui-même.
type linkFiles struct {
	repository, caCert, password, cache string
}

// resticOutcome est ce qu'une exécution de restic laisse : le résumé s'il est
// venu, la fin filtrée de sa sortie d'erreur.
type resticOutcome struct {
	summary restic.BackupSummary
	lines   []agentwire.OutputLine
}

// backup fait restic.backup. Avant chaque exécution, la source sonde la
// cible (TCP puis TLS épinglé) : joignable, restic part en direct ; sinon il
// part par le relais du serveur, sans que ce soit un échec. Le résultat dit
// toujours le chemin pris et pourquoi.
func (s *resticSource) backup(ctx context.Context, job resticBackupJob, checks hostChecks) (agentwire.ResticRunReport, []agentwire.OutputLine, error) {
	ctx, cancel := context.WithTimeout(ctx, resticBackupTimeout)
	defer cancel()
	startedAt := s.now()
	producer, err := s.producerFor(ctx, job, checks)
	if err != nil {
		return agentwire.ResticRunReport{}, nil, err
	}
	files, secrets, err := s.prepareLink(job.params.Target)
	if isUntouched(err) {
		return agentwire.ResticRunReport{}, nil, err
	}
	if err != nil {
		return agentwire.ResticRunReport{}, nil, refuseWith(agentwire.ReasonPathUnwritable, err)
	}
	var choice restic.Choice
	var outcome resticOutcome
	for attempt := 1; attempt <= resticAttempts; attempt++ {
		choice = restic.Choose(ctx, s.dialer, s.loadTarget(job.params.Target), job.params.ForceRelay)
		s.logger.Info("restic path chosen", "target", job.params.Target, "path", choice.Path, "reason", choice.Reason, "attempt", attempt)
		outcome, err = s.runOnce(ctx, job, choice, files, producer)
		if err == nil || ctx.Err() != nil {
			break
		}
	}
	report := runReportOf(choice, outcome.summary, s.now().Sub(startedAt))
	output := append([]agentwire.OutputLine{resticOutput("path " + choice.Path + " " + choice.Reason)}, redactLines(outcome.lines, secrets, redactedSecret)...)
	if err != nil {
		return report, output, failWith(agentwire.ReasonResticFailed, err)
	}
	return report, append(output, resticOutput("snapshot "+report.SnapshotID)), nil
}

// producerFor rend, pour un invité, la commande que restic lance et lit
// (--stdin-from-command) : vzdump par le script de root, comme backup.produce,
// sans compression. Le droit se rejuge sur la machine avant de lancer.
func (s *resticSource) producerFor(ctx context.Context, job resticBackupJob, checks hostChecks) ([]string, error) {
	if job.params.Scope != agentwire.ResticScopeVM {
		return nil, nil
	}
	if reason := producerReason(ctx, checks, machine.ProducerProxmoxVzdump); reason != "" {
		return nil, refuseWith(reason, errors.New("the producer cannot run on this machine"))
	}
	sudo, err := checks.lookPath("sudo")
	if err != nil || !filepath.IsAbs(sudo) {
		return nil, refuseWith(agentwire.ReasonSudoMissing, errors.New("sudo not found as an absolute path"))
	}
	vmid := strconv.Itoa(job.params.VMID)
	return append([]string{sudo}, vzdumpArguments(brand.VzdumpWrapperPath, vmid, vzdumpCompressNone)...), nil
}

// prepareLink rend les fichiers du lien et ses secrets, à masquer dans toute
// sortie. Sans les mots de passe de restic.provision, rien ne part.
func (s *resticSource) prepareLink(target string) (linkFiles, []string, error) {
	link, err := s.openLinkDir(target)
	if err != nil {
		return linkFiles{}, nil, err
	}
	defer link.Close()
	repoPassword, err := readLinkSecret(link, linkRepoPassword)
	if err != nil {
		return linkFiles{}, nil, refuseWith(agentwire.ReasonResticNotProvisioned, err)
	}
	secrets := []string{repoPassword}
	if restPassword, err := readLinkSecret(link, linkRESTPassword); err == nil {
		secrets = append(secrets, restPassword)
	}
	if err := link.Mkdir(linkCacheDir, resticDirMode); err != nil && !errors.Is(err, fs.ErrExist) {
		return linkFiles{}, nil, fmt.Errorf("create cache dir: %w", err)
	}
	base, err := absoluteStatePath(s.stateDir, resticDir, linksDir, target)
	if err != nil {
		return linkFiles{}, nil, err
	}
	files := linkFiles{
		repository: filepath.Join(base, linkRepositoryFile),
		caCert:     filepath.Join(base, linkCACertFile),
		password:   filepath.Join(base, linkRepoPassword),
		cache:      filepath.Join(base, linkCacheDir),
	}
	return files, secrets, nil
}

// runOnce lance restic une fois sur le chemin choisi ; un dépôt absent (la
// première sauvegarde) se crée, puis la sauvegarde repart, sans compter pour
// un rejeu.
func (s *resticSource) runOnce(ctx context.Context, job resticBackupJob, choice restic.Choice, files linkFiles, producer []string) (resticOutcome, error) {
	repository, release, err := s.repositoryFor(choice, job.params.Target, files)
	if err != nil {
		return resticOutcome{}, err
	}
	defer release()
	command, err := backupCommand(job, repository, producer)
	if err != nil {
		return resticOutcome{}, err
	}
	outcome, code, err := s.execute(ctx, command)
	if err != nil || code != restic.ExitRepositoryMissing {
		return outcome, judgeExit(code, outcome, err)
	}
	init, err := restic.InitRepository(job.binary, repository)
	if err != nil {
		return outcome, err
	}
	initOutcome, initCode, err := s.execute(ctx, init)
	if err != nil {
		return initOutcome, fmt.Errorf("run restic init: %w", err)
	}
	if initCode != 0 {
		return initOutcome, fmt.Errorf("restic init exited with code %d", initCode)
	}
	outcome, code, err = s.execute(ctx, command)
	return outcome, judgeExit(code, outcome, err)
}

// judgeExit : une sauvegarde réussit quand restic sort en 0 ou 3 et a écrit
// son résumé.
func judgeExit(code int, outcome resticOutcome, err error) error {
	if err != nil {
		return fmt.Errorf("run restic: %w", err)
	}
	if (code == 0 || code == resticExitIncomplete) && outcome.summary.SnapshotID != "" {
		return nil
	}
	return fmt.Errorf("restic exited with code %d", code)
}

// repositoryFor rend le dépôt du chemin choisi. Par le relais, le mandataire
// local vit le temps d'une exécution ; release le ferme.
func (s *resticSource) repositoryFor(choice restic.Choice, target string, files linkFiles) (restic.Repository, func(), error) {
	repository := restic.Repository{Path: choice.Path, PasswordFile: files.password, CacheDir: files.cache}
	if choice.Path == agentwire.ResticPathDirect {
		repository.RepositoryFile, repository.CACertFile = files.repository, files.caCert
		return repository, func() {}, nil
	}
	if s.relay == nil {
		return restic.Repository{}, nil, errors.New("no relay to reach the target")
	}
	config, err := s.relay(target)
	if err != nil {
		return restic.Repository{}, nil, err
	}
	proxy := restic.NewProxy(config)
	proxyURL, err := proxy.Start()
	if err != nil {
		return restic.Repository{}, nil, err
	}
	repository.ProxyURL = proxyURL
	return repository, func() { _ = proxy.Close() }, nil
}

// backupCommand rend la commande de la portée : le dossier du service, ou le
// flux de vzdump sous un nom stable (restic déduplique d'une passe à l'autre).
func backupCommand(job resticBackupJob, repository restic.Repository, producer []string) (restic.Command, error) {
	if job.params.Scope == agentwire.ResticScopeService {
		return restic.BackupDirectory(job.binary, repository, job.directory)
	}
	extension := "vma"
	if job.params.GuestType == agentwire.GuestLXC {
		extension = "tar"
	}
	name := "vzdump-" + job.params.GuestType + "-" + strconv.Itoa(job.params.VMID) + "." + extension
	return restic.BackupStream(job.binary, repository, name, producer)
}

// execute lance restic ; la sortie standard ne sert qu'au résumé, la sortie
// d'erreur n'en garde que la fin, nettoyée.
func (s *resticSource) execute(ctx context.Context, command restic.Command) (resticOutcome, int, error) {
	summary := &summaryWriter{}
	tail := newLineTail(resticTailLines)
	code, err := s.runner.run(ctx, command, summary, tail.writer("stderr"))
	tail.flush()
	found, _ := summary.result()
	return resticOutcome{summary: found, lines: tail.lines()}, code, err
}

// runReportOf rend le bilan de l'exécution ; un identifiant d'instantané hors
// forme est retiré plutôt que de faire refuser tout le résultat.
func runReportOf(choice restic.Choice, summary restic.BackupSummary, elapsed time.Duration) agentwire.ResticRunReport {
	report := choice.Report()
	report.SnapshotID = summary.SnapshotID
	report.BytesAdded = max(summary.DataAdded, 0)
	report.DurationSeconds = int64(elapsed.Seconds())
	if report.Validate() != nil {
		report.SnapshotID = ""
	}
	return report
}
