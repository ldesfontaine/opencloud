package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
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
// backup.produce pour un invité, restic.docker pour des volumes ou des bases,
// restic présent, et pour un service son dossier sous la racine. Un refus ne
// lance rien.
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
	if needsDocker(params) && !machine.HasCapability(capabilities, machine.CapabilityResticDocker) {
		return resticBackupJob{}, refused(agentwire.RightMissing(machine.CapabilityResticDocker))
	}
	binary, err := checks.findRestic()
	if err != nil {
		return resticBackupJob{}, refused(agentwire.ReasonResticMissing)
	}
	job := resticBackupJob{params: params, binary: binary}
	// Un service sans dossier (ses seuls volumes et bases) n'a pas cette partie.
	if params.Scope == agentwire.ResticScopeService && params.Service != "" {
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
// Les volumes nommés de Docker n'y sont pas : ce sont des parties à part.
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

// backup fait restic.backup, partie par partie. Avant chaque passe, la
// source sonde la cible (TCP puis TLS épinglé) : joignable, restic part en
// direct ; sinon il part par le relais du serveur, sans que ce soit un échec.
// Le résultat dit toujours le chemin pris et pourquoi.
func (s *resticSource) backup(ctx context.Context, job resticBackupJob, checks hostChecks) (agentwire.ResticRunReport, []agentwire.OutputLine, error) {
	ctx, cancel := context.WithTimeout(ctx, resticBackupTimeout)
	defer cancel()
	startedAt := s.now()
	parts, err := s.partsFor(ctx, job, checks)
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
	choice := s.runParts(ctx, job, parts, files)
	report := runReportOf(choice, parts, s.now().Sub(startedAt))
	return report, partsOutput(choice, parts, secrets), partsError(parts)
}

// partsFor rend ce que l'ordre sauvegarde : le flux de vzdump pour un
// invité, les parties d'un service sinon.
func (s *resticSource) partsFor(ctx context.Context, job resticBackupJob, checks hostChecks) ([]backupPart, error) {
	if job.params.Scope == agentwire.ResticScopeService {
		return s.serviceParts(ctx, job, checks)
	}
	producer, err := s.producerFor(ctx, job, checks)
	if err != nil {
		return nil, err
	}
	return []backupPart{{filename: guestFileName(job.params), producer: producer}}, nil
}

// runParts lance les parties l'une après l'autre ; une passe rejoue celles
// qui ont échoué, sur un chemin rechoisi. Le chemin se choisit une fois par
// passe, le même pour toutes ses parties.
func (s *resticSource) runParts(ctx context.Context, job resticBackupJob, parts []backupPart, files linkFiles) restic.Choice {
	var choice restic.Choice
	for attempt := 1; attempt <= resticAttempts; attempt++ {
		choice = restic.Choose(ctx, s.dialer, s.loadTarget(job.params.Target), job.params.ForceRelay)
		s.logger.Info("restic path chosen", "target", job.params.Target, "path", choice.Path, "reason", choice.Reason, "attempt", attempt)
		for index := range parts {
			if parts[index].isPending() && ctx.Err() == nil {
				s.runPart(ctx, job, choice, files, &parts[index])
			}
		}
		if ctx.Err() != nil || !slices.ContainsFunc(parts, backupPart.isPending) {
			break
		}
	}
	// Une partie que l'arrêt a empêchée de partir a échoué.
	for index := range parts {
		if parts[index].isRunnable() && parts[index].report.State == "" {
			parts[index].report.State, parts[index].report.Reason = agentwire.ResticPartFailed, agentwire.ReasonResticFailed
		}
	}
	return choice
}

// runPart lance une partie et note ce qu'elle a donné.
func (s *resticSource) runPart(ctx context.Context, job resticBackupJob, choice restic.Choice, files linkFiles, part *backupPart) {
	outcome, err := s.runOnce(ctx, job, choice, files, *part)
	part.lines = outcome.lines
	if err != nil {
		part.report.State, part.report.Reason = agentwire.ResticPartFailed, failureReason(*part)
		part.report.BytesAdded, part.snapshotID = 0, ""
		s.logger.Warn("restic part failed", "kind", part.report.Kind, "name", part.report.Name, "reason", part.report.Reason, "error", err)
		return
	}
	part.report.State, part.report.Reason = agentwire.ResticPartOK, ""
	part.report.BytesAdded, part.snapshotID = max(outcome.summary.DataAdded, 0), outcome.summary.SnapshotID
	s.logger.Info("restic part saved", "kind", part.report.Kind, "name", part.report.Name)
}

// producerFor rend, pour un invité, la commande que restic lance et lit
// (--stdin-from-command) : vzdump par le script de root, comme backup.produce,
// sans compression. Le droit se rejuge sur la machine avant de lancer.
func (s *resticSource) producerFor(ctx context.Context, job resticBackupJob, checks hostChecks) ([]string, error) {
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

// guestFileName est le nom stable du flux d'un invité : restic déduplique
// d'une passe à l'autre.
func guestFileName(params agentwire.ResticBackupParams) string {
	extension := "vma"
	if params.GuestType == agentwire.GuestLXC {
		extension = "tar"
	}
	return "vzdump-" + params.GuestType + "-" + strconv.Itoa(params.VMID) + "." + extension
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
func (s *resticSource) runOnce(ctx context.Context, job resticBackupJob, choice restic.Choice, files linkFiles, part backupPart) (resticOutcome, error) {
	repository, release, err := s.repositoryFor(choice, job.params.Target, files)
	if err != nil {
		return resticOutcome{}, err
	}
	defer release()
	command, err := part.command(job.binary, repository)
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

// runReportOf rend le bilan de l'exécution : le chemin, la somme de ce que
// les parties ont écrit, le dernier instantané réussi et, pour un service,
// le bilan de chaque partie. Un identifiant d'instantané hors forme est
// retiré plutôt que de faire refuser tout le résultat.
func runReportOf(choice restic.Choice, parts []backupPart, elapsed time.Duration) agentwire.ResticRunReport {
	report := choice.Report()
	for _, part := range parts {
		report.BytesAdded += part.report.BytesAdded
		if part.report.State == agentwire.ResticPartOK {
			report.SnapshotID = part.snapshotID
		}
		if part.report.Kind != "" {
			report.Parts = append(report.Parts, part.report)
		}
	}
	report.DurationSeconds = int64(elapsed.Seconds())
	if report.Validate() != nil {
		report.SnapshotID = ""
	}
	return report
}

// partsOutput rend la sortie de l'ordre : le chemin, puis pour chaque partie
// la fin filtrée de la sortie d'erreur de restic et son bilan. Les secrets
// du lien sont masqués ; ceux d'une base ne passent jamais par l'agent.
func partsOutput(choice restic.Choice, parts []backupPart, secrets []string) []agentwire.OutputLine {
	output := []agentwire.OutputLine{resticOutput("path " + choice.Path + " " + choice.Reason)}
	budget := partTailBudget(len(parts))
	for _, part := range parts {
		lines := part.lines[max(len(part.lines)-budget, 0):]
		output = append(output, redactLines(lines, secrets, redactedSecret)...)
		if line := part.statusLine(); line != "" {
			output = append(output, resticOutput(line))
		}
	}
	return output[:min(len(output), agentwire.MaxOutputLines)]
}

// partTailBudget partage les lignes d'un résultat entre les parties : une
// ligne de chemin, une de bilan par partie, le reste pour leur sortie.
func partTailBudget(count int) int {
	if count <= 1 {
		return resticTailLines
	}
	return max((agentwire.MaxOutputLines-1-count)/count, 0)
}

// statusLine dit ce qu'une partie a donné ; un invité ne dit que son
// instantané.
func (p backupPart) statusLine() string {
	label := p.label()
	switch {
	case p.report.State == agentwire.ResticPartOK && label == "":
		return "snapshot " + p.snapshotID
	case p.report.State == agentwire.ResticPartOK:
		return label + " snapshot " + p.snapshotID
	case label == "":
		return ""
	}
	return label + " " + p.report.State + " " + p.report.Reason
}

// partsError rend l'échec de l'ordre : la raison de la première partie en
// échec ; si aucune n'a rien sauvegardé, celle de la première ignorée.
func partsError(parts []backupPart) error {
	saved, skippedReason := false, ""
	for _, part := range parts {
		switch part.report.State {
		case agentwire.ResticPartFailed:
			return failWith(part.report.Reason, errors.New("a backup part failed"))
		case agentwire.ResticPartOK:
			saved = true
		case agentwire.ResticPartSkipped:
			if skippedReason == "" {
				skippedReason = part.report.Reason
			}
		}
	}
	if saved {
		return nil
	}
	if skippedReason == "" {
		skippedReason = agentwire.ReasonResticFailed
	}
	return failWith(skippedReason, errors.New("nothing was backed up"))
}
