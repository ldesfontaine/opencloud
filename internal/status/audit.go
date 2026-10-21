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
		Details: audit.Details{"page_id": written.PageID, "members": strconv.Itoa(len(written.Members))},
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

// pageEvent nomme la page par son slug, son adresse : le titre peut être
// vide. L'annonce est déjà publique, elle se garde telle quelle.
func pageEvent(action audit.Action, written Page) audit.Event {
	return audit.Event{
		Action: action, ObjectID: written.ID, ObjectName: written.Slug,
		Details: audit.Details{
			"title": written.Title, "announcement": written.Announcement,
			"language": string(written.Language), "project_id": written.ProjectID,
		},
	}
}
