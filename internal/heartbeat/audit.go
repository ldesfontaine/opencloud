package heartbeat

import (
	"strconv"

	"github.com/ldesfontaine/opencloud/internal/audit"
)

// SetAuditor branche le journal d'audit ; nil est toléré.
func (s *Service) SetAuditor(auditor *audit.Recorder) {
	s.auditor = auditor
}

// heartbeatEvent dit ce qu'un geste a écrit. Jamais le jeton : il est l'URL
// de ping, donc le secret de la tâche.
func heartbeatEvent(action audit.Action, written Heartbeat) audit.Event {
	return audit.Event{
		Action: action, ObjectID: written.ID, ObjectName: written.Name,
		Details: audit.Details{
			"machine_id":       written.MachineID,
			"interval_seconds": strconv.Itoa(int(written.Interval.Seconds())),
			"grace_seconds":    strconv.Itoa(int(written.Grace.Seconds())),
		},
	}
}
