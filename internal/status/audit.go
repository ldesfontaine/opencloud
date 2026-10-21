package status

import (
	"strconv"

	"github.com/ldesfontaine/opencloud/internal/audit"
)

// SetAuditor branche le journal d'audit ; nil est toléré.
func (s *Service) SetAuditor(auditor *audit.Recorder) {
	s.auditor = auditor
}

func componentEvent(action audit.Action, written Component) audit.Event {
	return audit.Event{
		Action: action, ObjectID: written.ID, ObjectName: written.Name,
		Details: audit.Details{"members": strconv.Itoa(len(written.Members))},
	}
}

func incidentEvent(action audit.Action, written Incident) audit.Event {
	return audit.Event{
		Action: action, ObjectID: written.ID, ObjectName: written.Title,
		Details: audit.Details{
			"impact":     string(written.Impact),
			"status":     string(written.Status),
			"components": strconv.Itoa(len(written.Components)),
		},
	}
}

// postEvent : une entrée du fil public. Le message est déjà public, il se
// garde tel quel.
func postEvent(written Incident, message string) audit.Event {
	return audit.Event{
		Action: audit.IncidentPost, ObjectID: written.ID, ObjectName: written.Title,
		Details: audit.Details{"status": string(written.Status), "message": message},
	}
}

func pageEvent(written Page) audit.Event {
	return audit.Event{
		Action: audit.StatusPageUpdate, ObjectName: written.Title,
		Details: audit.Details{"announcement": written.Announcement, "language": string(written.Language)},
	}
}
