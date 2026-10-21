package mcp

import (
	"strconv"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
)

// SetAuditor branche le journal d'audit ; nil est toléré.
func (s *Service) SetAuditor(auditor *audit.Recorder) {
	s.auditor = auditor
}

// clientEvent vise le client OAuth, qui n'a qu'une instance. Jamais son
// secret.
func clientEvent(action audit.Action, details audit.Details) audit.Event {
	return audit.Event{Action: action, ObjectID: ClientID, ObjectName: ClientID, Details: details}
}

// apiTokenEvent dit le nom, la portée et l'échéance ; ni le jeton ni son
// préfixe.
func apiTokenEvent(created Token) audit.Event {
	expires := "never"
	if !created.ExpiresAt.IsZero() {
		expires = created.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return audit.Event{
		Action: audit.MCPTokenCreate, ObjectID: created.ID, ObjectName: created.Name,
		Details: audit.Details{"scope": string(created.Scope), "expires_at": expires},
	}
}

// consentEvent dit la réponse de l'opérateur : le client, où il revient,
// et la portée accordée (ou demandée, pour un refus). Pas encore de famille :
// elle naît à l'échange du code.
func consentEvent(action audit.Action, consent Consent, scope Scope) audit.Event {
	return audit.Event{
		Action: action, ObjectName: consent.ClientID,
		Details: audit.Details{"client_id": consent.ClientID, "redirect_uri": consent.RedirectURI, "scope": string(scope)},
	}
}

func revokeAllEvent(revoked Revoked) audit.Event {
	return audit.Event{
		Action: audit.MCPTokenRevoke,
		Details: audit.Details{
			"all":        "true",
			"api_tokens": strconv.Itoa(revoked.APITokens),
			"sessions":   strconv.Itoa(revoked.Sessions),
		},
	}
}
