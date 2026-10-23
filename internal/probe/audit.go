package probe

import "github.com/ldesfontaine/opencloud/internal/audit"

// SetAuditor branche le journal d'audit ; nil est toléré.
func (s *Service) SetAuditor(auditor *audit.Recorder) {
	s.auditor = auditor
}

// probeEvent dit ce qu'un geste a écrit, cible masquée : une sonde d'avant
// le refus des identifiants peut encore en porter.
func probeEvent(action audit.Action, written Probe) audit.Event {
	return audit.Event{
		Action: action, ObjectID: written.ID, ObjectName: written.Name,
		Details: audit.Details{
			"kind":       string(written.Kind),
			"target":     RedactTarget(written.Target),
			"machine_id": written.MachineID,
			"service_id": written.ServiceID,
		},
	}
}

func pauseAction(wasPaused bool) audit.Action {
	if wasPaused {
		return audit.ProbeResume
	}
	return audit.ProbePause
}
