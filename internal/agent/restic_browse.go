package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/restic"
)

// Le parcours d'un dépôt, chez la cible, pour l'explorateur de la page
// Sauvegardes : les instantanés, ou le contenu d'un dossier. Le mot de passe du
// dépôt arrive dans le secret signé de l'ordre, écrit le temps de la commande
// puis effacé. Ne sortent que des noms, des tailles et des dates, par la liste
// blanche de agentwire.ResticBrowseReport ; jamais la sortie brute de restic,
// jamais un contenu.
const (
	resticBrowseTimeout = 90 * time.Second
	// Un dossier très profond : au-delà, les lignes lues sans fils direct sont
	// ignorées (restic peut lister tout le sous-arbre).
	maxBrowseScanLines = 2_000_000
)

type resticBrowseJob struct {
	params     agentwire.ResticBrowseParams
	password   string
	binary     string
	repository string
}

// acceptResticBrowse juge l'ordre vite (forme, secret, droit restic.browse,
// restic présent), puis l'accepte : la lecture est le travail du worker. Un
// dépôt occupé par une rétention ou une vérification n'empêche pas de lire.
func (r *orderRunner) acceptResticBrowse(body agentwire.OrderBody) (agentwire.ActionResult, func(ctx context.Context)) {
	dir, isTarget := resticTargetDir(r.identity.Capabilities())
	if !isTarget {
		r.logger.Warn("order not accepted", "run_id", body.RunID, "kind", string(body.Kind), "reason", agentwire.RightMissing(machine.CapabilityResticTarget))
		return agentwire.ActionResult{RunID: body.RunID, State: agentwire.ActionRefused, Reason: agentwire.RightMissing(machine.CapabilityResticTarget)}, nil
	}
	job, refused := judgeBrowse(body, dir, r.checks)
	if refused != nil {
		r.logger.Warn("order not accepted", "run_id", body.RunID, "kind", string(body.Kind), "reason", refused.Reason)
		return *refused, nil
	}
	return accepted(body), func(ctx context.Context) {
		report, err := r.restic.target.browse(ctx, job)
		result := r.finishRestic(body, nil, err)
		if err == nil {
			result.Browse = &report
		}
		r.results.push(result)
	}
}

func judgeBrowse(body agentwire.OrderBody, dir string, checks hostChecks) (resticBrowseJob, *agentwire.ActionResult) {
	refused := func(state agentwire.ActionState, reason string) *agentwire.ActionResult {
		return &agentwire.ActionResult{RunID: body.RunID, State: state, Reason: reason}
	}
	var params agentwire.ResticBrowseParams
	if err := json.Unmarshal(body.Params, &params); err != nil || params.Validate() != nil {
		return resticBrowseJob{}, refused(agentwire.ActionFailed, agentwire.ReasonBadOrder)
	}
	var secret agentwire.ResticBrowseSecret
	if err := json.Unmarshal(body.Secret, &secret); err != nil || secret.Validate() != nil {
		return resticBrowseJob{}, refused(agentwire.ActionFailed, agentwire.ReasonBadOrder)
	}
	binary, err := checks.findRestic()
	if err != nil {
		return resticBrowseJob{}, refused(agentwire.ActionRefused, agentwire.ReasonResticMissing)
	}
	return resticBrowseJob{params: params, password: secret.RepoPassword, binary: binary, repository: filepath.Join(dir, params.User)}, nil
}

// browse lit ce que l'ordre demande. Le fichier du mot de passe est effacé
// quoi qu'il arrive.
func (t *resticTarget) browse(ctx context.Context, job resticBrowseJob) (agentwire.ResticBrowseReport, error) {
	ctx, cancel := context.WithTimeout(ctx, resticBrowseTimeout)
	defer cancel()
	passwordFile, remove, err := t.writeRepoPassword("browse-", job.params.User, job.password)
	if err != nil {
		return agentwire.ResticBrowseReport{}, failWith(agentwire.ReasonPathUnwritable, err)
	}
	defer remove()
	var report agentwire.ResticBrowseReport
	if job.params.Snapshot == "" {
		report.Snapshots, err = t.browseSnapshots(ctx, job, passwordFile)
	} else {
		report.Entries, report.Truncated, err = t.browseDirectory(ctx, job, passwordFile)
	}
	if err != nil {
		return agentwire.ResticBrowseReport{}, err
	}
	if report.Validate() != nil {
		return agentwire.ResticBrowseReport{}, failWith(agentwire.ReasonResticFailed, errors.New("browse report outside its bounds"))
	}
	return report, nil
}

func (t *resticTarget) browseSnapshots(ctx context.Context, job resticBrowseJob, passwordFile string) ([]agentwire.BrowseSnapshot, error) {
	command, err := restic.AllSnapshots(job.binary, job.repository, passwordFile)
	if err != nil {
		return nil, failWith(agentwire.ReasonBadOrder, err)
	}
	var listing limitedBuffer
	listing.limit = maxSnapshotsOutput
	code, runErr := t.runner.run(ctx, command, &listing, discard{})
	if isRepositoryMissing(code) {
		return nil, failWith(agentwire.ReasonRepoEmpty, errors.New("the repository does not exist yet"))
	}
	if runErr != nil || code != 0 || listing.overflowed {
		return nil, failWith(agentwire.ReasonResticFailed, fmt.Errorf("list snapshots: code %d: %v", code, runErr))
	}
	snapshots, err := restic.ParseBrowseSnapshots(listing.Bytes(), agentwire.MaxBrowseSnapshots)
	if err != nil {
		return nil, failWith(agentwire.ReasonResticFailed, fmt.Errorf("parse snapshots: %w", err))
	}
	return snapshots, nil
}

func (t *resticTarget) browseDirectory(ctx context.Context, job resticBrowseJob, passwordFile string) ([]agentwire.BrowseEntry, bool, error) {
	command, err := restic.ListDirectory(job.binary, job.repository, passwordFile, job.params.Snapshot, job.params.Path)
	if err != nil {
		return nil, false, failWith(agentwire.ReasonBadOrder, err)
	}
	collector := &directoryCollector{directory: job.params.Path}
	code, runErr := t.runner.run(ctx, command, collector, discard{})
	if collector.stopped() {
		// Les bornes sont atteintes : la lecture a été coupée, la liste est partielle.
		return collector.sorted(), true, nil
	}
	if runErr != nil || code != 0 {
		return nil, false, failWith(agentwire.ReasonResticFailed, fmt.Errorf("list directory: code %d: %v", code, runErr))
	}
	return collector.sorted(), false, nil
}

// discard jette une sortie ; la sortie d'erreur de restic peut citer des noms,
// elle ne va nulle part.
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// directoryCollector lit restic ls --json au fil de l'eau et ne garde que les
// fils directs du dossier, au plus MaxBrowseEntries.
type directoryCollector struct {
	directory string
	pending   []byte
	skipping  bool
	scanned   int
	entries   []agentwire.BrowseEntry
	overflow  bool
}

func (c *directoryCollector) Write(data []byte) (int, error) {
	for _, b := range data {
		switch {
		case b == '\n':
			c.endLine()
		case c.skipping:
		case len(c.pending) >= maxNodeLine:
			c.pending, c.skipping = c.pending[:0], true
		default:
			c.pending = append(c.pending, b)
		}
	}
	return len(data), nil
}

func (c *directoryCollector) endLine() {
	defer func() { c.pending, c.skipping = c.pending[:0], false }()
	c.scanned++
	if c.skipping || c.stopped() {
		return
	}
	entry, isChild := restic.ParseDirectory(bytes.Clone(c.pending), c.directory)
	if !isChild {
		return
	}
	if len(c.entries) >= agentwire.MaxBrowseEntries {
		c.overflow = true
		return
	}
	c.entries = append(c.entries, entry)
}

// stopped : le dossier a plus de fils que la borne, ou la lecture a trop
// parcouru de lignes. Au-delà, on lit sans rien garder.
func (c *directoryCollector) stopped() bool {
	return c.overflow || c.scanned > maxBrowseScanLines
}

// sorted : les dossiers d'abord, puis par nom.
func (c *directoryCollector) sorted() []agentwire.BrowseEntry {
	entries := slices.Clone(c.entries)
	slices.SortFunc(entries, func(a, b agentwire.BrowseEntry) int {
		if (a.Kind == "dir") != (b.Kind == "dir") {
			if a.Kind == "dir" {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return entries
}
