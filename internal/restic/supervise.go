package restic

import (
	"context"
	"slices"
	"time"

	"github.com/ldesfontaine/opencloud/internal/agentwire"
	"github.com/ldesfontaine/opencloud/internal/backup"
)

// La supervision des sauvegardes vit dans ce module (décision de Lucas du
// 01/11/2026) : ni dans « Tâches », ni par des webhooks Proxmox. Elle lit
// l'aperçu de la page Sauvegardes, la même source que l'écran, et en tire des
// constats que le moteur des alertes ouvre, aggrave ou reprend.
const (
	// Aucune réussite depuis plus de ceci : attention ; au-delà de la
	// seconde borne : danger. 36 h, comme « En retard » à l'écran.
	StaleAfter       = staleAfter
	StaleDangerAfter = 7 * 24 * time.Hour
	// Un test de restauration plus vieux que VerifyStaleAfter alerte ; plus
	// vieux que ceci, c'est un danger.
	VerifyDangerAfter = 30 * 24 * time.Hour
	// La place que doit offrir une cible : au moins ceci libre, et deux fois
	// le plus gros ajout récent. Moins que le plus gros ajout : danger.
	TargetMinFree = int64(1) << 30
)

// FindingKind nomme un constat de la supervision.
type FindingKind string

const (
	FindingStale             FindingKind = "backup_stale"
	FindingRestoreStale      FindingKind = "restore_test_stale"
	FindingSourceUnprotected FindingKind = "source_unprotected"
	FindingTargetFull        FindingKind = "target_full"
	FindingTargetUnreachable FindingKind = "target_unreachable"
)

// Les raisons d'un constat, une liste fermée que les catalogues traduisent.
const (
	ReasonTargetOffline = "offline"
	ReasonTargetStopped = "stopped"
	ReasonTargetError   = "error"
	// Le droit restic.verify manque à la cible, ou elle ne peut pas l'exercer.
	ReasonVerifyRightMissing     = "verify_right_missing"
	ReasonVerifyRightUnavailable = "verify_right_unavailable"
	ReasonNeverVerified          = "never_verified"
)

// Finding est un constat : un objet (une protection, une source, une cible)
// et ce qui cloche. Des faits, pas des phrases : le moteur et l'écran les
// disent dans la langue de l'interface.
type Finding struct {
	Kind   FindingKind
	Danger bool
	// ObjectID est stable d'un passage à l'autre : c'est la clé de l'alerte.
	ObjectID   string
	ObjectName string
	// La machine dont l'alerte dépend (sa disparition la résout) : la source,
	// ou la cible.
	MachineID string
	ServiceID string
	// Depuis quand : la dernière réussite, ou la création quand il n'y en a
	// pas eu.
	Since time.Time
	// La raison fermée : celle de l'échec, de la cible, du droit manquant.
	Reason string
	// Count : les sauvegardes qu'une cible garde ; Bytes : sa place libre.
	Count int
	Bytes int64
}

// Supervise juge l'aperçu et donne les constats au moteur des alertes, qui
// reprend ceux qu'il ne reçoit plus. Sans moteur branché, il ne fait rien.
func (s *Service) Supervise(ctx context.Context) {
	if s.alerter == nil {
		return
	}
	overview, err := s.Overview(ctx)
	if err != nil {
		s.logger.Error("judge backups", "error", err)
		return
	}
	s.alerter.BackupsJudged(ctx, FindingsOf(overview, s.now()))
}

// FindingsOf tire les constats d'un aperçu. Tant qu'aucune protection
// n'existe, le module n'est pas en service : rien ne manque encore, pas même
// un plan.
func FindingsOf(overview Overview, now time.Time) []Finding {
	findings := []Finding{}
	inService := false
	for _, source := range overview.Sources {
		if len(source.Protections) > 0 {
			inService = true
		}
		for _, protection := range source.Protections {
			findings = append(findings, protectionFindings(source, protection, now)...)
		}
	}
	findings = withoutRepeatedRepositories(findings)
	if inService {
		for _, source := range overview.Sources {
			if finding, ok := unprotectedFinding(source); ok {
				findings = append(findings, finding)
			}
		}
	}
	for _, target := range overview.Targets {
		if target.Kind == TargetKindRestic {
			findings = append(findings, targetFindings(target, overview.Sources)...)
		}
	}
	return findings
}

// protectionFindings : pas de réussite depuis trop longtemps, test de
// restauration périmé. Les tâches vzdump que Proxmox mène encore (observées)
// ne sont pas jugées : openCloud ne connaît pas leur rythme.
func protectionFindings(source Source, protection Protection, now time.Time) []Finding {
	if protection.Origin != OriginRestic && protection.Origin != OriginArchive {
		return nil
	}
	objectID := protection.Origin + ":" + protection.ID
	findings := []Finding{}
	since := protection.LastSuccessAt
	if since.IsZero() {
		since = protection.CreatedAt
	}
	if !since.IsZero() && now.Sub(since) > StaleAfter {
		findings = append(findings, Finding{
			Kind: FindingStale, Danger: now.Sub(since) > StaleDangerAfter, ObjectID: objectID, ObjectName: protection.Name,
			MachineID: source.MachineID, ServiceID: source.ServiceID, Since: since, Reason: failureReason(protection),
		})
	}
	if finding, ok := restoreStaleFinding(source, protection, now); ok {
		findings = append(findings, finding)
	}
	return findings
}

// failureReason : « failed » ou « refused » quand la dernière tentative a
// échoué ou été refusée (la carte dit pourquoi), vide sinon.
func failureReason(protection Protection) string {
	if protection.State == ProtectionFailed || protection.State == ProtectionRefused {
		return protection.State
	}
	return ""
}

// restoreStaleFinding : le test est celui du dépôt (une source vers une
// cible), pas de chaque sauvegarde : l'alerte porte le dépôt, nommé par ses
// deux machines. Avant la première réussite, l'attente court depuis la plus
// ancienne sauvegarde du dépôt (VerifyView.Since).
func restoreStaleFinding(source Source, protection Protection, now time.Time) (Finding, bool) {
	verify := protection.Verify
	if verify == nil || !verify.IsStale {
		return Finding{}, false
	}
	since := verify.SuccessAt
	if since.IsZero() {
		since = verify.Since
	}
	finding := Finding{
		Kind: FindingRestoreStale, Danger: now.Sub(since) > VerifyDangerAfter, ObjectID: "repo:" + source.MachineID + ">" + protection.TargetMachineID,
		ObjectName: source.MachineName + " → " + protection.TargetMachineName, MachineID: source.MachineID, Since: since, Reason: verifyReason(*verify),
	}
	return finding, true
}

// verifyReason : ce qui retient la vérification, dans l'ordre où l'opérateur
// le corrige : le droit de la cible, puis l'échec du dernier essai.
func verifyReason(verify VerifyView) string {
	switch verify.Right.State {
	case RightMissing:
		return ReasonVerifyRightMissing
	case RightUnavailable:
		return ReasonVerifyRightUnavailable
	}
	switch {
	case verify.State == StateFailed || verify.State == StateRefused:
		return verify.Reason
	case verify.SuccessAt.IsZero():
		return ReasonNeverVerified
	}
	return ""
}

// unprotectedFinding : une base, un invité, un pare-feu ou un service que rien ne couvre.
// Un volume appartient à son service. Une source dont l'hôte n'a pas su
// lire les sauvegardes n'est pas « sans plan » : on ne sait pas.
func unprotectedFinding(source Source) (Finding, bool) {
	judged := []string{backup.KindDatabase, backup.KindVM, backup.KindLXC, backup.KindFirewall, agentwire.ResticScopeService}
	if !slices.Contains(judged, source.Kind) || len(source.Protections) > 0 || source.ObservedUnknown {
		return Finding{}, false
	}
	return Finding{
		Kind: FindingSourceUnprotected, ObjectID: "source:" + source.Key, ObjectName: source.Name,
		MachineID: source.MachineID, ServiceID: source.ServiceID,
	}, true
}

// targetFindings : une cible qui garde des sauvegardes et ne peut plus les
// recevoir (machine hors ligne, rest-server arrêté ou en erreur), ou qui n'a
// plus la place du prochain ajout.
func targetFindings(target TargetView, sources []Source) []Finding {
	kept := keptBy(target, sources)
	if len(kept) == 0 {
		return nil
	}
	if reason := unreachableReason(target); reason != "" {
		return []Finding{{
			Kind: FindingTargetUnreachable, Danger: true, ObjectID: "target:" + target.MachineID, ObjectName: target.Name,
			MachineID: target.MachineID, Reason: reason, Count: len(kept),
		}}
	}
	if target.State != agentwire.ResticTargetRunning || target.DiskTotal <= 0 {
		return nil
	}
	largest := int64(0)
	for _, protection := range kept {
		if protection.AddedBytes != nil {
			largest = max(largest, *protection.AddedBytes)
		}
	}
	if target.DiskFree >= max(2*largest, TargetMinFree) {
		return nil
	}
	return []Finding{{
		Kind: FindingTargetFull, Danger: target.DiskFree < max(largest, TargetMinFree/4), ObjectID: "target:" + target.MachineID,
		ObjectName: target.Name, MachineID: target.MachineID, Count: len(kept), Bytes: target.DiskFree,
	}}
}

// keptBy rend les sauvegardes restic que la cible reçoit.
func keptBy(target TargetView, sources []Source) []Protection {
	kept := []Protection{}
	for _, source := range sources {
		for _, protection := range source.Protections {
			if protection.Origin == OriginRestic && protection.TargetMachineID == target.MachineID {
				kept = append(kept, protection)
			}
		}
	}
	return kept
}

func unreachableReason(target TargetView) string {
	switch {
	case !target.Online:
		return ReasonTargetOffline
	case target.State == agentwire.ResticTargetStopped:
		return ReasonTargetStopped
	case target.State == agentwire.ResticTargetError:
		return ReasonTargetError
	}
	return ""
}

// withoutRepeatedRepositories ne garde qu'un constat de test par dépôt : les
// sauvegardes d'un même dépôt le partagent.
func withoutRepeatedRepositories(findings []Finding) []Finding {
	seen := map[string]bool{}
	kept := findings[:0]
	for _, finding := range findings {
		if finding.Kind == FindingRestoreStale {
			if seen[finding.ObjectID] {
				continue
			}
			seen[finding.ObjectID] = true
		}
		kept = append(kept, finding)
	}
	return kept
}
