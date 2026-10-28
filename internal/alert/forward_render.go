package alert

import (
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/lang"
)

// journalPath est la page du journal dans l'interface, sous public_url.
const journalPath = "/journal"

// JournalLink est l'adresse de la page du journal, ou rien sans public_url.
func JournalLink(publicURL string) string {
	if publicURL == "" {
		return ""
	}
	return strings.TrimRight(publicURL, "/") + journalPath
}

// auditPayload est le corps JSON d'un événement du journal : l'événement
// tel que la base le garde, jamais un secret (les détails sont déjà filtrés).
type auditPayload struct {
	Event    string     `json:"event"` // "audit"
	SentAt   time.Time  `json:"sent_at"`
	Language string     `json:"language"`
	Title    string     `json:"title"`
	Audit    auditEntry `json:"audit"`
	URL      string     `json:"url,omitempty"`
}

type auditEntry struct {
	ID        int64     `json:"id"`
	At        time.Time `json:"at"`
	ActorKind string    `json:"actor_kind"`
	ActorName string    `json:"actor_name"`
	// ActorLabel : le nom du jeton d'API à l'instant du geste ; omis sinon.
	ActorLabel string            `json:"actor_label,omitempty"`
	Action     string            `json:"action"`
	ObjectKind string            `json:"object_kind"`
	ObjectID   string            `json:"object_id"`
	ObjectName string            `json:"object_name"`
	Details    map[string]string `json:"details"`
}

// RenderAudit fait le corps d'un événement du journal pour un format : qui,
// quoi, sur quel objet, en une phrase courte. Les détails n'y figurent que
// dans le corps JSON, qui est fait pour être lu par une machine.
func RenderAudit(catalog lang.Catalog, format Format, event audit.Event, now time.Time, link string) (Body, error) {
	escape := escaperOf(format)
	if escape == nil {
		escape = func(text string) string { return text }
	}
	fact := catalog.Get("audit.verb_"+event.Action.Verb()) + " · " + catalog.Get("audit.object_"+string(event.ObjectKind))
	if event.ObjectName != "" {
		fact += " « " + escape(event.ObjectName) + " »"
	}
	actor := catalog.Get("audit.actor_" + string(event.Actor.Kind))
	instant := event.At.UTC().Format(time.RFC3339)
	cause := catalog.Format("audit.notify_by_kind", actor, instant)
	// Le nom du jeton parle mieux que son identifiant.
	who := event.Actor.Name
	if event.Actor.Label != "" {
		who = event.Actor.Label
	}
	if who != "" {
		cause = catalog.Format("audit.notify_by", actor, escape(who), instant)
	}
	title := catalog.Get("alert.notify_audit") + " · " + fact
	text := Text{Fact: fact, Cause: cause}
	openLabel := openLinkLabel(catalog)
	// Le journal n'est pas une alerte : les formats qui colorent par gravité
	// le montrent en « attention ».
	alert := Alert{Severity: SeverityAttention}
	switch format {
	case FormatText:
		return Body{Content: []byte(plainText(title, text)), ContentType: "text/plain; charset=utf-8", Title: title, Link: link}, nil
	case FormatDiscord:
		return jsonBody(title, link, discordPayload(title, text, alert, EventAudit, now, link, openLabel))
	case FormatSlack:
		return jsonBody(title, link, slackPayload(title, text, alert, EventAudit, link, openLabel))
	case FormatEmail:
		return emailBody(title, text, link, openLabel), nil
	case FormatMatrix:
		return matrixBody(title, text, link, openLabel)
	}
	details := event.Details
	if details == nil {
		details = map[string]string{}
	}
	return jsonBody(title, link, auditPayload{
		Event: string(EventAudit), SentAt: now.UTC(), Language: string(catalog.Code()), Title: title, URL: link,
		Audit: auditEntry{
			ID: event.ID, At: event.At.UTC(), ActorKind: string(event.Actor.Kind), ActorName: event.Actor.Name, ActorLabel: event.Actor.Label,
			Action: string(event.Action), ObjectKind: string(event.ObjectKind), ObjectID: event.ObjectID,
			ObjectName: event.ObjectName, Details: details,
		},
	})
}
