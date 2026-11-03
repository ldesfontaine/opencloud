package alert

import (
	"context"

	"github.com/ldesfontaine/opencloud/internal/restic"
)

// Ce que le module Sauvegardes constate : les constats de sa supervision, à
// chaque passage. Un constat ouvre ou aggrave l'alerte de son objet ; celle
// dont le constat ne revient plus se résout. Les cinq types sont à elle seule :
// aucun autre composant n'ouvre ni ne reprend ces alertes.
func (e *Engine) BackupsJudged(ctx context.Context, findings []restic.Finding) {
	seen := map[string]bool{}
	for _, finding := range findings {
		fact := backupFact(finding)
		seen[string(fact.Kind)+"\x00"+fact.Object.ID] = true
		e.Open(ctx, fact)
	}
	for _, kind := range backupKinds() {
		open, err := e.store.ListAlerts(ctx, Filter{Status: StatusOpen, Kind: kind, ObjectKind: ObjectBackup})
		if err != nil {
			e.fail(ctx, "list backup alerts", err)
			return
		}
		for _, alert := range open {
			if !seen[string(kind)+"\x00"+alert.Object.ID] {
				e.Resolve(ctx, kind, alert.Object)
			}
		}
	}
}

func backupKinds() []Kind {
	return []Kind{KindBackupStale, KindRestoreTestStale, KindSourceUnprotected, KindTargetFull, KindTargetUnreachable}
}

func backupFact(finding restic.Finding) Fact {
	severity := SeverityAttention
	if finding.Danger {
		severity = SeverityDanger
	}
	return Fact{
		Kind: Kind(finding.Kind), Severity: severity, MachineID: finding.MachineID, ServiceID: finding.ServiceID,
		Object:  Object{Kind: ObjectBackup, ID: finding.ObjectID, Name: finding.ObjectName},
		Details: Details{Since: finding.Since, Reason: finding.Reason, Count: finding.Count, Bytes: finding.Bytes},
	}
}
