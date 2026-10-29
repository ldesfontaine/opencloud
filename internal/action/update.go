package action

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/release"
)

// ErrUpdateUnavailable : l'agent n'a pas de mise à jour à recevoir en ce
// moment ; la fiche de la machine dit pourquoi (UpdateOffer.Reason).
var ErrUpdateUnavailable = refusal.Conflict("action.update_unavailable", "the agent cannot be updated now: its offer says why")

// ErrRollbackUnavailable : l'agent ne sait pas revenir seul à sa version
// d'avant (il n'est pas migré vers le lanceur stable, ou plus vieux que le
// retour arrière). Aucune mise à jour ne lui est envoyée, sans exception.
var ErrRollbackUnavailable = refusal.Conflict("action.rollback_unavailable", "the agent cannot return to its previous version by itself: migrate it first")

// ErrUpdateInProgress : une mise à jour d'agent, de cette machine ou d'une
// autre, n'est ni confirmée, ni en échec, ni échue. Une à la fois, pour
// l'opérateur comme pour un jeton.
var ErrUpdateInProgress = refusal.Conflict("action.update_in_progress", "another agent update is not confirmed yet: wait for it to end")

// UpdateInFlightWindow : tant qu'un ordre agent.update ou agent.rollback n'a
// pas fini depuis moins que cela, il compte comme en vol. Le téléchargement
// (vingt minutes au plus chez l'agent), le délai de confirmation (trois
// minutes) et une marge ; au-delà, l'ordre est échu et ne bloque plus rien.
const UpdateInFlightWindow = 30 * time.Minute

// Les raisons pour lesquelles une machine n'est pas à mettre à jour. same_version
// est la seule sans phrase : une machine à jour n'a rien à dire.
const (
	OfferSameVersion    = "same_version"
	OfferNewerAgent     = "newer_agent"
	OfferNoRelease      = "no_release"
	OfferReleaseInvalid = "release_invalid"
	OfferNoAgent        = "no_agent"
	OfferOffline        = "offline"
	OfferRightMissing   = "right_missing"
	OfferArchUnknown    = "arch_unknown"
	// L'agent ne rapporte pas de retour arrière automatique : il doit être
	// migré une fois à la main avant sa prochaine mise à jour.
	OfferRollbackUnavailable = "rollback_unavailable"
	// Pas de bibliothèque de versions : la commande mcp n'en a pas, et un
	// binaire sans clé de version ne peut rien vérifier.
	OfferNoReleases = "no_releases"
	// Le droit est coché et rapporté, mais le compte de l'agent ne peut pas
	// l'exercer. Seule raison qui accompagne une offre disponible : le serveur
	// l'affiche sans bloquer le bouton, l'agent refuse avec sa raison.
	OfferRightUnavailable = "right_unavailable"
)

// OfferReasons rend les raisons que l'interface dit à l'opérateur, une phrase
// chacune ; same_version n'en a pas.
func OfferReasons() []string {
	return []string{
		OfferNewerAgent, OfferNoRelease, OfferReleaseInvalid, OfferNoAgent,
		OfferOffline, OfferRightMissing, OfferRightUnavailable, OfferArchUnknown, OfferNoReleases,
		OfferRollbackUnavailable,
	}
}

// Releases est ce que le composant lit de la bibliothèque des versions
// importées : le binaire d'une version pour une architecture, revérifié à
// chaque appel (release.Library).
type Releases interface {
	Artifact(version release.Version, arch string) (release.Artifact, error)
}

// UpdateOffer dit si l'agent d'une machine peut recevoir la version du
// serveur, et sinon pourquoi.
type UpdateOffer struct {
	Current   string
	Target    string
	Arch      string
	Available bool
	// Vide quand la mise à jour est disponible, sauf OfferRightUnavailable :
	// elle l'est quand même.
	Reason string
}

// SetReleases branche la bibliothèque des versions et dit quelle version le
// serveur est : c'est la seule que ses agents reçoivent. Une version du
// serveur illisible coupe la mise à jour des agents.
func (s *Service) SetReleases(releases Releases, serverVersion string) {
	parsed, err := release.ParseVersion(serverVersion)
	if err != nil {
		s.logger.Warn("server version unreadable: agent updates are off", "version", serverVersion)
		s.releases = nil
		return
	}
	s.releases, s.serverVersion = releases, parsed
}

// archPattern : une architecture comme Go la nomme (amd64, arm64, riscv64).
var archPattern = regexp.MustCompile(`^[a-z0-9]{2,16}$`)

// OfferUpdate dit ce que la fiche de la machine propose : les raisons sont
// jugées dans cet ordre, la première qui tient est rendue.
func (s *Service) OfferUpdate(ctx context.Context, machineID string) (UpdateOffer, error) {
	offer, _, err := s.judgeUpdate(ctx, machineID)
	return offer, err
}

func (s *Service) judgeUpdate(ctx context.Context, machineID string) (UpdateOffer, release.Artifact, error) {
	if s.releases == nil {
		return UpdateOffer{Reason: OfferNoReleases}, release.Artifact{}, nil
	}
	target, err := s.machines.Get(ctx, machineID)
	if err != nil {
		return UpdateOffer{}, release.Artifact{}, err
	}
	return s.judgeMachine(target, nil)
}

// artifactCache garde, le temps d'une liste de machines, ce que la
// bibliothèque a rendu par architecture : la revérifier coûte la lecture du
// binaire, et dix machines de la même architecture n'en demandent qu'une.
type artifactCache map[string]cachedArtifact

type cachedArtifact struct {
	artifact release.Artifact
	err      error
}

func (s *Service) judgeMachine(target machine.Status, cache artifactCache) (UpdateOffer, release.Artifact, error) {
	if s.releases == nil {
		return UpdateOffer{Reason: OfferNoReleases}, release.Artifact{}, nil
	}
	offer := UpdateOffer{Target: s.serverVersion.String()}
	if !target.Agent.IsActive() {
		offer.Reason = OfferNoAgent
		return offer, release.Artifact{}, nil
	}
	offer.Current, offer.Arch = target.Agent.Version, target.Arch
	if reason := s.machineReason(target); reason != "" {
		offer.Reason = reason
		return offer, release.Artifact{}, nil
	}
	if reason := s.versionReason(target.Agent.Version); reason != "" {
		offer.Reason = reason
		return offer, release.Artifact{}, nil
	}
	// Après « à jour » : une machine à jour n'a rien à migrer pour l'instant.
	// Avant la bibliothèque : migrer ne dépend pas de ce qui est importé.
	if !target.Agent.CanRollBack() {
		offer.Reason = OfferRollbackUnavailable
		return offer, release.Artifact{}, nil
	}
	artifact, err := s.artifactFor(target.Arch, cache)
	if errors.Is(err, release.ErrNoRelease) {
		offer.Reason = OfferNoRelease
		return offer, release.Artifact{}, nil
	}
	if err != nil {
		s.logger.Warn("imported release unusable", "version", offer.Target, "arch", target.Arch, "error", err)
		offer.Reason = OfferReleaseInvalid
		return offer, release.Artifact{}, nil
	}
	offer.Available = true
	if target.Agent.UnavailableReason(machine.CapabilityAgentUpdate) != "" {
		offer.Reason = OfferRightUnavailable
	}
	return offer, artifact, nil
}

func (s *Service) artifactFor(arch string, cache artifactCache) (release.Artifact, error) {
	if found, ok := cache[arch]; ok {
		return found.artifact, found.err
	}
	artifact, err := s.releases.Artifact(s.serverVersion, arch)
	if cache != nil {
		cache[arch] = cachedArtifact{artifact: artifact, err: err}
	}
	return artifact, err
}

// machineReason juge ce qui tient à la machine et à son agent : en ligne, le
// droit rapporté, l'architecture. Tant que l'agent n'a rien rapporté, c'est
// lui qui juge le droit.
func (s *Service) machineReason(target machine.Status) string {
	switch {
	case !target.Online:
		return OfferOffline
	case !target.Agent.ReportedAt.IsZero() && !machine.HasCapability(target.Agent.Capabilities, machine.CapabilityAgentUpdate):
		return OfferRightMissing
	case !archPattern.MatchString(target.Arch):
		return OfferArchUnknown
	}
	return ""
}

// versionReason compare l'agent au serveur. Une version d'agent illisible
// compte comme plus ancienne : la mise à jour la corrige.
func (s *Service) versionReason(agentVersion string) string {
	current, err := release.ParseVersion(agentVersion)
	if err != nil {
		return ""
	}
	switch current.Compare(s.serverVersion) {
	case 0:
		return OfferSameVersion
	case 1:
		return OfferNewerAgent
	}
	return ""
}

// refusalOf rend l'erreur d'une offre qui ne tient pas : celles que l'API dit
// déjà (hors ligne, droit, pas d'agent), sinon ErrUpdateUnavailable.
func refusalOf(reason string) error {
	switch reason {
	case OfferOffline:
		return ErrMachineOffline
	case OfferRightMissing:
		return ErrRightMissing
	case OfferNoAgent:
		return machine.ErrNoAgent
	case OfferRollbackUnavailable:
		return ErrRollbackUnavailable
	}
	return ErrUpdateUnavailable
}

// UpdateAgent envoie à l'agent de la machine l'ordre agent.update : le
// binaire de la version du serveur, pris dans la bibliothèque et revérifié
// maintenant, part en colis avec les sommes et leur signature. L'agent
// revérifie tout avec sa propre clé ; l'ordre reste « accepté » jusqu'à son
// retour (ConfirmAgentUpdate).
func (s *Service) UpdateAgent(ctx context.Context, machineID string) (Run, error) {
	// Une à la fois : juger « rien en vol » et écrire le run se font sous le
	// même verrou, sans quoi deux demandes simultanées passeraient ensemble.
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	offer, artifact, err := s.judgeUpdate(ctx, machineID)
	if err != nil {
		return Run{}, err
	}
	if !offer.Available {
		return Run{}, refusalOf(offer.Reason)
	}
	if err := s.refuseWhileUpdating(ctx); err != nil {
		return Run{}, err
	}
	if s.outbox == nil {
		return Run{}, ErrUpdateUnavailable
	}
	writer, err := s.outbox.Create(machineID)
	if err != nil {
		return Run{}, fmt.Errorf("create parcel: %w", err)
	}
	params, err := s.stageUpdate(writer, artifact)
	if err != nil {
		writer.Discard()
		return Run{}, err
	}
	// La version de départ et celle d'arrivée entrent dans l'audit : un agent
	// ou un opérateur qui lit le journal sait de quoi à quoi.
	run, err := s.send(ctx, ServiceOrder{
		MachineID: machineID, Kind: agentwire.KindAgentUpdate, Params: params,
		AuditDetails: audit.Details{"from": offer.Current, "to": offer.Target},
	})
	if err != nil || run.State.IsFinished() {
		// L'ordre n'est pas parti : personne ne viendra chercher le colis.
		writer.Discard()
	}
	return run, err
}

// stageUpdate met dans le colis les trois fichiers de la version, empruntés à
// la bibliothèque (ni copiés ni effacés), et rend les paramètres de l'ordre.
func (s *Service) stageUpdate(writer *ParcelWriter, artifact release.Artifact) (json.RawMessage, error) {
	for name, statePath := range map[string]string{
		artifact.Name:              artifact.Path,
		brand.ReleaseSumsFile:      artifact.SumsPath,
		brand.ReleaseSignatureFile: artifact.SignaturePath,
	} {
		if err := writer.Borrow(name, statePath); err != nil {
			return nil, fmt.Errorf("add %s to the parcel: %w", name, err)
		}
	}
	parcelID, files := writer.Seal()
	// La somme du colis est celle que le fichier a au moment d'être emprunté :
	// un binaire changé depuis la vérification ne part pas.
	for _, file := range files {
		if file.Name == artifact.Name && (file.SHA256 != artifact.SHA256 || file.Size != artifact.Size) {
			return nil, fmt.Errorf("%w: %s changed while the parcel was prepared", ErrUpdateUnavailable, artifact.Name)
		}
	}
	params, err := json.Marshal(agentwire.AgentUpdateParams{
		Version: artifact.Version.String(), Parcel: parcelID, Binary: artifact.Name, Files: files,
	})
	if err != nil {
		return nil, fmt.Errorf("encode update params: %w", err)
	}
	return params, nil
}

// ConfirmAgentUpdate ferme les mises à jour de cet agent quand il revient avec
// la version visée : le serveur ne croit pas qu'un agent a changé de binaire
// avant de le lui entendre dire à l'ouverture de son flux. Une version
// annoncée qui n'est celle d'aucun ordre en attente ne change rien.
func (s *Service) ConfirmAgentUpdate(ctx context.Context, machineID, agentID, reported string) error {
	if reported == "" {
		return nil
	}
	runs, err := s.store.CompleteAgentUpdates(ctx, agentID, reported, s.now())
	if err != nil {
		return fmt.Errorf("complete agent updates: %w", err)
	}
	for _, run := range runs {
		s.logger.Info("agent updated", "run_id", run.ID, "machine_id", machineID, "version", reported)
	}
	if len(runs) > 0 {
		// Une mise à jour confirmée referme l'alerte « revenu à la version
		// précédente » de cette machine : elle a avancé.
		if s.alerter != nil {
			s.alerter.AgentUpdated(ctx, machineID)
		}
		s.changed(machineID)
	}
	return nil
}
