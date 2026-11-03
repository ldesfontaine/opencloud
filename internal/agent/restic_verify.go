package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/restic"
)

// La vérification d'un dépôt, chez la cible, sans rien saisir : restic check,
// puis la relecture d'un échantillon de fichiers du dernier instantané de
// chaque ensemble de chemins (restic dump, vers une sortie qui compte les
// octets et les jette). Le mot de passe du dépôt arrive dans le secret signé
// de l'ordre, écrit le temps des commandes puis effacé. Rien du contenu n'est
// écrit sur le disque ni rapporté : le serveur reçoit des comptes.
const (
	resticVerifyTimeout = 55 * time.Minute
	// Un dump dure au plus ceci ; il s'arrête de lui-même à la borne d'octets.
	resticDumpTimeout = 10 * time.Minute
	// Les instantanés relus : les plus récents, au plus ce nombre.
	verifySnapshotsMax = 8
	// Les octets relus par fichier : de quoi traverser plusieurs blocs
	// déchiffrés, sans lire une VM entière.
	verifyBytesPerFile = 16 << 20
	// La sortie de restic snapshots et la longueur d'une ligne de ls : au-delà,
	// la ligne est ignorée.
	maxSnapshotsOutput = 4 << 20
	maxNodeLine        = 64 << 10
)

// resticVerifyJob est un ordre restic.verify jugé.
type resticVerifyJob struct {
	user       string
	password   string
	binary     string
	repository string
}

// acceptResticVerify juge l'ordre vite (forme, secret, droit restic.verify,
// restic présent, dépôt libre), puis l'accepte : la vérification est le
// travail du worker.
func (r *orderRunner) acceptResticVerify(body agentwire.OrderBody) (agentwire.ActionResult, func(ctx context.Context)) {
	dir, isTarget := resticTargetDir(r.identity.Capabilities())
	if !isTarget {
		// Le dépôt est sous le dossier du droit restic.target : sans lui, aucun
		// dépôt à vérifier.
		r.logger.Warn("order not accepted", "run_id", body.RunID, "kind", string(body.Kind), "reason", agentwire.RightMissing(machine.CapabilityResticTarget))
		return agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionRefused, Reason: agentwire.RightMissing(machine.CapabilityResticTarget)}, nil
	}
	job, refused := r.restic.target.judgeVerify(body, dir, r.checks)
	if refused != nil {
		r.logger.Warn("order not accepted", "run_id", body.RunID, "kind", string(body.Kind), "reason", refused.Reason)
		return *refused, nil
	}
	return accepted(body), func(ctx context.Context) {
		report, output, err := r.restic.target.verify(ctx, job)
		result := r.finishRestic(body, output, err)
		if err == nil {
			result.Verify = &report
		}
		r.results.push(result)
	}
}

// judgeVerify lit l'ordre et le refuse sans rien lancer s'il ne passe pas.
func (t *resticTarget) judgeVerify(body agentwire.OrderBody, dir string, checks hostChecks) (resticVerifyJob, *agentwire.ActionResult) {
	refused := func(state agentwire.ActionState, reason string) *agentwire.ActionResult {
		return &agentwire.ActionResult{RunID: body.RunID, State: state, Reason: reason}
	}
	var params agentwire.ResticVerifyParams
	if err := json.Unmarshal(body.Params, &params); err != nil || params.Validate() != nil {
		return resticVerifyJob{}, refused(agentwire.ActionFailed, agentwire.ReasonBadOrder)
	}
	var secret agentwire.ResticVerifySecret
	if err := json.Unmarshal(body.Secret, &secret); err != nil || secret.Validate() != nil {
		return resticVerifyJob{}, refused(agentwire.ActionFailed, agentwire.ReasonBadOrder)
	}
	binary, err := checks.findRestic()
	if err != nil {
		return resticVerifyJob{}, refused(agentwire.ActionRefused, agentwire.ReasonResticMissing)
	}
	if t.isRepoBusy(params.User) {
		return resticVerifyJob{}, refused(agentwire.ActionRefused, agentwire.ReasonBusy)
	}
	return resticVerifyJob{
		user: params.User, password: secret.RepoPassword, binary: binary, repository: filepath.Join(dir, params.User),
	}, nil
}

// verify mène la vérification : check, instantanés, relecture. Le mot de passe
// vit dans un fichier 0600 le temps de l'ordre ; il est effacé quoi qu'il
// arrive et masqué dans toute sortie.
func (t *resticTarget) verify(ctx context.Context, job resticVerifyJob) (agentwire.ResticVerifyReport, []agentwire.OutputLine, error) {
	if !t.claimMaintenance(job.user) {
		return agentwire.ResticVerifyReport{}, nil, refuseWith(agentwire.ReasonBusy, errors.New("the repository is busy"))
	}
	defer t.releaseMaintenance(job.user)
	ctx, cancel := context.WithTimeout(ctx, resticVerifyTimeout)
	defer cancel()
	passwordFile, remove, err := t.writeRepoPassword("verify-", job.user, job.password)
	if err != nil {
		return agentwire.ResticVerifyReport{}, nil, failWith(agentwire.ReasonPathUnwritable, err)
	}
	defer remove()
	check := verification{target: t, job: job, passwordFile: passwordFile}
	output, err := check.checkStructure(ctx)
	output = redactLines(output, []string{job.password}, redactedSecret)
	if err != nil {
		return agentwire.ResticVerifyReport{}, output, err
	}
	report, err := check.readSamples(ctx)
	if err != nil {
		return agentwire.ResticVerifyReport{}, output, err
	}
	output = append(output, resticOutput(fmt.Sprintf("read %d files in %d snapshots, %d bytes", report.Files, report.Snapshots, report.Bytes)))
	return report, output, nil
}

// verification tient ce que les étapes partagent : la cible, l'ordre, le
// fichier du mot de passe.
type verification struct {
	target       *resticTarget
	job          resticVerifyJob
	passwordFile string
}

// isRepositoryMissing : le dépôt n'existe pas encore (restic 0.17 et après).
func isRepositoryMissing(code int) bool {
	return code == restic.ExitRepositoryMissing
}

// checkStructure lance restic check. Un dépôt abîmé : check_failed, avec la
// fin de la sortie de restic, qui nomme des paquets, jamais des fichiers.
func (v verification) checkStructure(ctx context.Context) ([]agentwire.OutputLine, error) {
	command, err := restic.CheckRepository(v.job.binary, v.job.repository, v.passwordFile)
	if err != nil {
		return nil, failWith(agentwire.ReasonBadOrder, err)
	}
	tail := newLineTail(resticTailLines)
	code, runErr := v.target.runner.run(ctx, command, tail.writer("stdout"), tail.writer("stderr"))
	tail.flush()
	output := append([]agentwire.OutputLine{resticOutput("check " + v.job.user)}, tail.lines()...)
	switch {
	case runErr != nil:
		return output, failWith(agentwire.ReasonResticFailed, fmt.Errorf("run restic check: %w", runErr))
	case isRepositoryMissing(code):
		return output, failWith(agentwire.ReasonRepoEmpty, errors.New("the repository does not exist yet"))
	case code != 0:
		return output, failWith(agentwire.ReasonCheckFailed, fmt.Errorf("restic check exited with code %d", code))
	}
	return output, nil
}

// readSamples relit, dans chacun des derniers instantanés, le plus petit
// fichier non vide et le plus gros. Aucun fichier relu : le dépôt est vide.
func (v verification) readSamples(ctx context.Context) (agentwire.ResticVerifyReport, error) {
	snapshots, err := v.latestSnapshots(ctx)
	if err != nil {
		return agentwire.ResticVerifyReport{}, err
	}
	var report agentwire.ResticVerifyReport
	for _, snapshot := range snapshots {
		files, err := v.sampleOf(ctx, snapshot.ID)
		if err != nil {
			return agentwire.ResticVerifyReport{}, err
		}
		if len(files) == 0 {
			continue
		}
		report.Snapshots++
		for _, file := range files {
			read, err := v.dump(ctx, snapshot.ID, file)
			if err != nil {
				return agentwire.ResticVerifyReport{}, err
			}
			report.Files++
			report.Bytes += read
		}
	}
	if report.Files == 0 {
		return agentwire.ResticVerifyReport{}, failWith(agentwire.ReasonRepoEmpty, errors.New("no file to read back in the repository"))
	}
	return report, report.Validate()
}

func (v verification) latestSnapshots(ctx context.Context) ([]restic.Snapshot, error) {
	command, err := restic.LatestSnapshots(v.job.binary, v.job.repository, v.passwordFile)
	if err != nil {
		return nil, failWith(agentwire.ReasonBadOrder, err)
	}
	var listing limitedBuffer
	listing.limit = maxSnapshotsOutput
	code, runErr := v.target.runner.run(ctx, command, &listing, io.Discard)
	if runErr != nil || code != 0 || listing.overflowed {
		return nil, failWith(agentwire.ReasonResticFailed, fmt.Errorf("list snapshots: code %d: %v", code, runErr))
	}
	snapshots, err := restic.ParseSnapshots(listing.Bytes(), verifySnapshotsMax)
	if err != nil {
		return nil, failWith(agentwire.ReasonResticFailed, fmt.Errorf("parse snapshots: %w", err))
	}
	if len(snapshots) == 0 {
		return nil, failWith(agentwire.ReasonRepoEmpty, errors.New("the repository has no snapshot"))
	}
	return snapshots, nil
}

// sampleOf choisit les fichiers à relire dans un instantané en lisant le
// listing au fil de l'eau : on ne garde que deux candidats.
func (v verification) sampleOf(ctx context.Context, snapshotID string) ([]restic.Node, error) {
	command, err := restic.ListSnapshot(v.job.binary, v.job.repository, v.passwordFile, snapshotID)
	if err != nil {
		return nil, failWith(agentwire.ReasonBadOrder, err)
	}
	picker := &samplePicker{}
	code, runErr := v.target.runner.run(ctx, command, picker, io.Discard)
	if runErr != nil || code != 0 {
		return nil, failWith(agentwire.ReasonResticFailed, fmt.Errorf("list snapshot: code %d: %v", code, runErr))
	}
	return picker.sample(), nil
}

// dump relit un fichier jusqu'à la borne d'octets et rend ce qui a été lu.
// Atteindre la borne arrête restic : c'est une réussite.
func (v verification) dump(ctx context.Context, snapshotID string, file restic.Node) (int64, error) {
	command, err := restic.DumpFile(v.job.binary, v.job.repository, v.passwordFile, snapshotID, file.Path)
	if err != nil {
		return 0, failWith(agentwire.ReasonBadOrder, err)
	}
	dumpCtx, cancel := context.WithTimeout(ctx, resticDumpTimeout)
	defer cancel()
	sink := &boundedSink{limit: min(file.Size, verifyBytesPerFile), stop: cancel}
	code, runErr := v.target.runner.run(dumpCtx, command, sink, io.Discard)
	if sink.reached() {
		return sink.read, nil
	}
	if runErr != nil || code != 0 || sink.read < min(file.Size, verifyBytesPerFile) {
		return 0, failWith(agentwire.ReasonRestoreFailed, fmt.Errorf("dump: code %d after %d bytes: %v", code, sink.read, runErr))
	}
	return sink.read, nil
}

// limitedBuffer garde la sortie d'une commande jusqu'à une borne ; au-delà,
// elle est jetée et overflowed le dit.
type limitedBuffer struct {
	bytes.Buffer
	limit      int
	overflowed bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		b.overflowed = true
		return len(p), nil
	}
	return b.Buffer.Write(p)
}

// boundedSink compte les octets que restic dump écrit et les jette. À la
// borne, il arrête la commande.
type boundedSink struct {
	limit int64
	read  int64
	stop  context.CancelFunc
}

func (s *boundedSink) Write(p []byte) (int, error) {
	s.read += int64(len(p))
	if s.reached() {
		s.read = s.limit
		s.stop()
	}
	return len(p), nil
}

func (s *boundedSink) reached() bool {
	return s.read >= s.limit
}

// samplePicker lit les lignes de restic ls --json au fil de l'eau et garde le
// plus petit fichier non vide et le plus gros.
type samplePicker struct {
	pending  []byte
	skipping bool
	smallest *restic.Node
	largest  *restic.Node
}

func (p *samplePicker) Write(data []byte) (int, error) {
	for _, b := range data {
		if b == '\n' {
			p.endLine()
			continue
		}
		if p.skipping {
			continue
		}
		if len(p.pending) >= maxNodeLine {
			p.pending, p.skipping = p.pending[:0], true
			continue
		}
		p.pending = append(p.pending, b)
	}
	return len(data), nil
}

func (p *samplePicker) endLine() {
	if !p.skipping {
		p.consider(p.pending)
	}
	p.pending, p.skipping = p.pending[:0], false
}

func (p *samplePicker) consider(line []byte) {
	node, isFile := restic.ParseNode(line)
	if !isFile || node.Size <= 0 {
		return
	}
	if p.smallest == nil || node.Size < p.smallest.Size {
		p.smallest = &node
	}
	if p.largest == nil || node.Size > p.largest.Size {
		p.largest = &node
	}
}

// sample rend les fichiers à relire, un seul si le plus petit est le plus gros.
func (p *samplePicker) sample() []restic.Node {
	if p.smallest == nil {
		return nil
	}
	if p.smallest.Path == p.largest.Path {
		return []restic.Node{*p.smallest}
	}
	return []restic.Node{*p.smallest, *p.largest}
}
