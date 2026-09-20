package probe

import "time"

// Changes est ce qu'un rapport change : les essais à écrire, et l'état de
// chaque sonde une fois qu'ils lui ont été appliqués.
type Changes struct {
	Now     time.Time
	Results []Result
	Probes  []Probe
}

// planChanges applique les essais aux sondes, dans l'ordre du rapport :
// chacun voit la sonde telle que le précédent l'a laissée. Une sonde que
// la machine ne porte plus est ignorée, ses essais avec.
func planChanges(known map[string]Probe, report Report, now time.Time) Changes {
	changes := Changes{Now: now}
	touched := make(map[string]int, len(report.Results))
	for _, result := range report.Results {
		current, ok := known[result.ProbeID]
		if !ok {
			continue
		}
		changes.Results = append(changes.Results, result)
		// Un essai rejoué est de l'histoire : il nourrit l'uptime et
		// s'arrête là. L'état, les compteurs et le certificat disent ce que
		// la sonde voit maintenant, et seul un essai vivant peut le dire.
		if result.Replayed {
			continue
		}
		after := advance(current, result)
		known[result.ProbeID] = after
		if place, seen := touched[result.ProbeID]; seen {
			changes.Probes[place] = after
			continue
		}
		touched[result.ProbeID] = len(changes.Probes)
		changes.Probes = append(changes.Probes, after)
	}
	return changes
}

// advance porte sur la sonde ce qu'un essai vient d'apprendre.
func advance(current Probe, result Result) Probe {
	after := current
	after.LastCheckedAt = result.CheckedAt
	after.LastDurationMs = result.DurationMs
	after.LastCode = result.Code
	after.LastReason = result.Reason
	if result.Certificate != nil {
		after.Certificate = result.Certificate
	}
	if result.Outcome.IsSuccess() {
		after.ConsecutiveSuccesses = current.ConsecutiveSuccesses + 1
		after.ConsecutiveFailures = 0
	} else {
		after.ConsecutiveFailures = current.ConsecutiveFailures + 1
		after.ConsecutiveSuccesses = 0
	}
	after.Status = nextStatus(current, after, result.Outcome)
	return after
}

// nextStatus ne fait basculer l'état qu'au seuil atteint : un hoquet
// réseau ne fait pas clignoter la page, et une cible qui revient doit le
// confirmer. Trois exceptions : une sonde en pause ne bouge pas, même si
// un essai parti avant la pause arrive après ; le premier essai fixe
// l'état tout de suite, parce qu'une sonde qui répond n'a pas à rester
// « Nouvelle » ; et Dégradé et En ligne sont deux nuances d'un même
// succès, passer de l'une à l'autre n'attend rien.
func nextStatus(before, after Probe, outcome Outcome) Status {
	switch before.Status {
	case StatusPaused:
		return StatusPaused
	case StatusNew:
		if outcome.IsSuccess() {
			return successStatus(outcome)
		}
		return StatusDown
	case StatusDown:
		if outcome.IsSuccess() && after.ConsecutiveSuccesses >= before.RecoveryThreshold {
			return successStatus(outcome)
		}
		return StatusDown
	default:
		if outcome.IsSuccess() {
			return successStatus(outcome)
		}
		if after.ConsecutiveFailures >= before.FailureThreshold {
			return StatusDown
		}
		return before.Status
	}
}

func successStatus(outcome Outcome) Status {
	if outcome == OutcomeDegraded {
		return StatusDegraded
	}
	return StatusUp
}
