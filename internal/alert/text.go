package alert

import (
	"slices"
	"time"

	"github.com/ldesfontaine/opencloud/internal/lang"
)

// Text est ce qu'une alerte dit, dans la langue de l'interface : le fait,
// la cause, puis ce qu'on peut faire. Le navigateur fait la même phrase
// avec les mêmes clés ; le canal reçoit celle-ci, rendue par le serveur.
type Text struct {
	Fact   string
	Cause  string
	Action string
}

// Describe rend les trois lignes d'une alerte. Un nom de machine absent,
// machine retirée ou tâche sans machine, se dit par un tiret.
func Describe(catalog lang.Catalog, alert Alert, now time.Time) Text {
	return describe(catalog, alert, now, nil)
}

// describe fait de même en passant par escape chaque morceau qui ne vient pas
// du catalogue : nom d'objet, de machine, cible, point de montage. Un agent
// choisit les noms qu'il rapporte, et un canal Discord ou Slack lirait son
// texte comme du balisage, à côté du lien qu'openCloud ajoute lui-même.
// escape nil laisse le texte tel quel.
func describe(catalog lang.Catalog, alert Alert, now time.Time, escape func(string) string) Text {
	if escape == nil {
		escape = func(text string) string { return text }
	}
	machine := alert.MachineName
	if machine == "" {
		machine = catalog.Get("alert.no_machine")
	}
	machine = escape(machine)
	object := escape(alert.Object.Name)
	details := alert.Details
	details.Target = escape(details.Target)
	details.MountPoint = escape(details.MountPoint)
	// Arrêté ou relancé, un service qui plante en boucle se dit pareil.
	if alert.IsLoop() {
		return Text{
			Fact:   catalog.Get("alert.fact_service_restart_loop"),
			Cause:  catalog.Format("alert.cause_service_restart_loop", object, machine, details.Count, details.ExitCode),
			Action: catalog.Get("alert.action_service_restart"),
		}
	}
	switch alert.Kind {
	case KindMachineLost:
		return text(catalog, "machine_lost", []any{}, []any{object, formatAge(catalog, now.Sub(details.Since))})
	case KindServiceDown:
		switch details.Reason {
		case ReasonOOM:
			return text(catalog, "service_down_oom", nil, []any{object, machine})
		case ReasonKilled:
			return text(catalog, "service_down_killed", nil, []any{object, machine})
		}
		return text(catalog, "service_down", nil, []any{object, machine, details.ExitCode})
	case KindServiceUnhealthy:
		return text(catalog, "service_unhealthy", nil, []any{object, machine})
	case KindServiceRestart:
		return text(catalog, "service_restart", nil, []any{object, machine, details.ExitCode})
	case KindJobLate:
		if details.Reason == ReasonNeverPinged {
			return text(catalog, "job_late_never", nil, []any{object, formatAge(catalog, now.Sub(details.Since))})
		}
		return text(catalog, "job_late", nil, []any{object})
	case KindJobFailed:
		return text(catalog, "job_failed", nil, []any{object, details.ExitCode})
	case KindProbeDown:
		return text(catalog, "probe_down", nil, []any{object, details.Target, catalog.Get("probe.reason_" + details.Reason)})
	case KindCertExpiring:
		return text(catalog, "cert_expiring", nil, []any{details.Target, daysUntil(details.NotAfter, now)})
	case KindCertExpired:
		return text(catalog, "cert_expired", nil, []any{details.Target, -daysUntil(details.NotAfter, now)})
	case KindCertUntrusted:
		return text(catalog, "cert_untrusted", nil, []any{details.Target, catalog.Get("alert.reason_" + details.Reason)})
	case KindAgentRolledBack:
		return text(catalog, "agent_rolled_back", nil, []any{machine, details.Target, catalog.Get("alert.rollback_cause_" + rollbackCauseOf(details))})
	case KindDiskFull:
		return text(catalog, "disk_full", []any{details.Percent}, []any{details.MountPoint, machine, diskThresholdOf(details)})
	}
	return Text{Fact: string(alert.Kind), Cause: object}
}

// rollbackCauseOf : la cause que dit l'alerte ; une cause inconnue se dit comme
// un délai passé.
func rollbackCauseOf(details Details) string {
	if slices.Contains(RollbackCauses(), details.Reason) {
		return details.Reason
	}
	return RollbackTimeout
}

// diskThresholdOf est le seuil que cite la phrase ; une alerte ouverte
// avant que le seuil ne soit noté cite celui par défaut.
func diskThresholdOf(details Details) int {
	if details.Threshold == 0 {
		return DefaultDiskAttentionPercent
	}
	return details.Threshold
}

func text(catalog lang.Catalog, kind string, factArgs, causeArgs []any) Text {
	return Text{
		Fact:   catalog.Format("alert.fact_"+kind, factArgs...),
		Cause:  catalog.Format("alert.cause_"+kind, causeArgs...),
		Action: catalog.Get("alert.action_" + kind),
	}
}

// daysUntil arrondit vers le haut ce qui reste, vers le bas ce qui est
// passé : « 1 jour » et « −1 jour » ne se confondent jamais, comme dans le
// navigateur.
func daysUntil(at, now time.Time) int {
	remaining := at.Sub(now)
	day := 24 * time.Hour
	if remaining >= 0 {
		return int((remaining + day - 1) / day)
	}
	return -int((-remaining + day - 1) / day)
}

// formatAge dit une durée avec les mots du catalogue : minutes sous
// l'heure, heures sous le jour, jours ensuite.
func formatAge(catalog lang.Catalog, age time.Duration) string {
	switch {
	case age < time.Hour:
		return catalog.Format("time.minutes", int(age.Minutes()))
	case age < 24*time.Hour:
		return catalog.Format("time.hours", int(age.Hours()))
	}
	return catalog.Format("time.days", int(age.Hours()/24))
}
