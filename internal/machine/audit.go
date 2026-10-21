package machine

import (
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
)

// SetAuditor branche le journal d'audit ; nil est toléré.
func (s *Service) SetAuditor(auditor *audit.Recorder) {
	s.auditor = auditor
}

func machineEvent(action audit.Action, written Machine) audit.Event {
	return audit.Event{
		Action: action, ObjectID: written.ID, ObjectName: written.Name,
		Details: audit.Details{"hostname": written.Hostname, "address": written.Address},
	}
}

// tokenEvent dit qu'un jeton d'enrôlement est émis : pour une machine
// nouvelle, l'objet est le jeton ; pour un ré-enrôlement, la machine dont la
// clé va changer. Jamais le jeton lui-même.
func tokenEvent(issued Token) audit.Event {
	expires := issued.ExpiresAt.UTC().Format(time.RFC3339)
	if issued.MachineID == "" {
		return audit.Event{
			Action: audit.EnrollmentTokenCreate, ObjectID: issued.ID, ObjectName: issued.Name,
			Details: audit.Details{"expires_at": expires},
		}
	}
	return audit.Event{
		Action: audit.MachineReenroll, ObjectID: issued.MachineID, ObjectName: issued.Name,
		Details: audit.Details{"token_id": issued.ID, "expires_at": expires},
	}
}
