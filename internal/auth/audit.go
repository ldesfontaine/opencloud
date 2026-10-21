package auth

import "github.com/ldesfontaine/opencloud/internal/audit"

// L'objet du journal : l'accès à l'interface, qui n'a qu'une instance.
const auditObjectID = "interface"

// SetAuditor branche le journal d'audit ; nil est toléré.
func (s *Service) SetAuditor(auditor *audit.Recorder) {
	s.auditor = auditor
}

// authEvent dit le mode, l'identifiant et l'en-tête ; jamais le mot de passe
// ni son empreinte.
func authEvent(action audit.Action, state State) audit.Event {
	return audit.Event{
		Action: action, ObjectID: auditObjectID, ObjectName: auditObjectID,
		Details: audit.Details{"mode": string(state.Mode), "username": state.Username, "header": state.Header},
	}
}
