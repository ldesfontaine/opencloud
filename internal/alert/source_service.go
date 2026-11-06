package alert

import (
	"context"
)

// Les deux visages de l'alerte d'une montée revenue en arrière : la version
// d'avant tourne, ou le retour a échoué aussi.
const (
	ReasonRolledBack     = "rolled_back"
	ReasonRollbackFailed = "rollback_failed"
)

// Ce que le composant des ordres constate d'une montée de version : elle n'a
// pas tenu et l'agent est revenu à la version d'avant, ou elle a fini par
// tenir. L'objet est le service déclaré : deux services d'une machine ont
// chacun leur alerte.

// ServiceRolledBack ouvre l'alerte ; danger quand le retour à la version d'avant
// a échoué aussi (le service ne tourne peut-être plus), attention sinon.
func (e *Engine) ServiceRolledBack(ctx context.Context, serviceID, serviceName, machineID, machineName, fromVersion, toVersion string, failed, late bool, journal []string) {
	severity, reason := SeverityAttention, ReasonRolledBack
	if failed {
		severity, reason = SeverityDanger, ReasonRollbackFailed
	}
	e.Open(ctx, Fact{
		Kind: KindServiceRolledBack, Severity: severity,
		Object:    Object{Kind: ObjectService, ID: serviceID, Name: serviceName},
		MachineID: machineID, ServiceID: serviceID,
		Details: Details{Reason: reason, Target: toVersion, Version: fromVersion, Log: journal, Late: late},
	})
}

// ServiceUpdated referme l'alerte : une montée a été confirmée depuis.
func (e *Engine) ServiceUpdated(ctx context.Context, serviceID string) {
	e.Resolve(ctx, KindServiceRolledBack, Object{Kind: ObjectService, ID: serviceID})
}
