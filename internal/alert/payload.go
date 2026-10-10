package alert

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/lang"
)

// Body est ce qu'un canal reçoit : le corps, son type, le titre et le
// lien que certains récepteurs lisent en en-tête.
type Body struct {
	Content     []byte
	ContentType string
	Title       string
	Link        string
}

// alertPath est la fiche d'une alerte dans l'interface, sous public_url.
const alertPath = "/alertes/alerte/"

// AlertLink est l'adresse de la fiche de l'alerte, ou rien : sans
// public_url, openCloud ne sait pas sous quelle adresse on le lit, et un
// test n'a pas de fiche.
func AlertLink(publicURL string, event Event, alert Alert) string {
	if publicURL == "" || event == EventTest || alert.ID == 0 {
		return ""
	}
	return strings.TrimRight(publicURL, "/") + alertPath + strconv.FormatInt(alert.ID, 10)
}

// AlertsLink est l'adresse de la page des alertes, ou rien sans public_url.
func AlertsLink(publicURL string) string {
	if publicURL == "" {
		return ""
	}
	return strings.TrimRight(publicURL, "/") + "/alertes"
}

// RenderSummary fait le message qui remplace ce que la limite de débit
// d'un canal a retenu : « N alertes de plus, voir openCloud ».
func RenderSummary(catalog lang.Catalog, format Format, held int, now time.Time, link string) (Body, error) {
	text := Text{Fact: catalog.Format("alert.summary_fact", held), Cause: catalog.Get("alert.summary_cause")}
	title := catalog.Get("alert.notify_summary") + " · " + text.Fact
	openLabel := catalog.Get("alert.open_in_opencloud")
	alert := Alert{Severity: SeverityAttention}
	switch format {
	case FormatText:
		return Body{Content: []byte(plainText(title, text)), ContentType: "text/plain; charset=utf-8", Title: title, Link: link}, nil
	case FormatDiscord:
		return jsonBody(title, link, discordPayload(title, text, alert, EventSummary, now, link, openLabel))
	case FormatSlack:
		return jsonBody(title, link, slackPayload(title, text, alert, EventSummary, link, openLabel))
	}
	body := payload{
		Event: string(EventSummary), SentAt: now.UTC(), Language: string(catalog.Code()), Title: title,
		Text: payloadText(text), Held: held, URL: link,
	}
	return jsonBody(title, link, body)
}

// Render fait le corps d'un événement pour un format. Le titre porte
// l'événement : « Alerte », « Alerte aggravée », « Résolu », « Test ».
// Le lien, quand il y en a un, mène à la fiche de l'alerte.
func Render(catalog lang.Catalog, format Format, event Event, alert Alert, now time.Time, link string) (Body, error) {
	text := Describe(catalog, alert, now)
	if event == EventTest {
		text = Text{Fact: catalog.Get("alert.test_fact"), Cause: catalog.Get("alert.test_cause"), Action: ""}
	}
	title := catalog.Get("alert.notify_"+string(event)) + " · " + text.Fact
	openLabel := catalog.Get("alert.open_in_opencloud")
	switch format {
	case FormatText:
		return Body{Content: []byte(plainText(title, text)), ContentType: "text/plain; charset=utf-8", Title: title, Link: link}, nil
	case FormatDiscord:
		return jsonBody(title, link, discordPayload(title, text, alert, event, now, link, openLabel))
	case FormatSlack:
		return jsonBody(title, link, slackPayload(title, text, alert, event, link, openLabel))
	}
	return jsonBody(title, link, openCloudPayload(catalog, title, text, alert, event, now, link))
}

func jsonBody(title, link string, payload any) (Body, error) {
	content, err := json.Marshal(payload)
	if err != nil {
		return Body{}, err
	}
	return Body{Content: content, ContentType: "application/json", Title: title, Link: link}, nil
}

func plainText(title string, text Text) string {
	lines := []string{title, text.Cause}
	if text.Action != "" {
		lines = append(lines, text.Action)
	}
	return strings.Join(lines, "\n")
}

// Le corps d'openCloud : les faits, tels que l'API les rend, et les trois
// lignes rendues dans la langue de l'interface.
type payload struct {
	Event    string      `json:"event"`
	SentAt   time.Time   `json:"sent_at"`
	Language string      `json:"language"`
	Title    string      `json:"title"`
	Text     payloadText `json:"text"`
	// Alert manque au corps d'un test et d'un résumé : il n'y a rien en alerte.
	Alert *payloadAlert `json:"alert,omitempty"`
	// Held : ce qu'un résumé remplace ; URL : la page des alertes.
	Held int    `json:"held,omitempty"`
	URL  string `json:"url,omitempty"`
}

type payloadText struct {
	Fact   string `json:"fact"`
	Cause  string `json:"cause"`
	Action string `json:"action,omitempty"`
}

type payloadAlert struct {
	ID         int64         `json:"id"`
	Kind       Kind          `json:"kind"`
	Severity   Severity      `json:"severity"`
	Status     Status        `json:"status"`
	Object     payloadObject `json:"object"`
	Machine    payloadObject `json:"machine"`
	Details    Details       `json:"details"`
	OpenedAt   time.Time     `json:"opened_at"`
	ResolvedAt *time.Time    `json:"resolved_at"`
	// URL manque sans public_url.
	URL string `json:"url,omitempty"`
}

type payloadObject struct {
	Kind string `json:"kind,omitempty"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

func openCloudPayload(catalog lang.Catalog, title string, text Text, alert Alert, event Event, now time.Time, link string) payload {
	body := payload{
		Event: string(event), SentAt: now.UTC(), Language: string(catalog.Code()), Title: title,
		Text: payloadText(text),
	}
	if event == EventTest {
		return body
	}
	var resolvedAt *time.Time
	if !alert.ResolvedAt.IsZero() {
		at := alert.ResolvedAt.UTC()
		resolvedAt = &at
	}
	body.Alert = &payloadAlert{
		ID: alert.ID, Kind: alert.Kind, Severity: alert.Severity, Status: alert.Status,
		Object:  payloadObject{Kind: string(alert.Object.Kind), ID: alert.Object.ID, Name: alert.Object.Name},
		Machine: payloadObject{ID: alert.MachineID, Name: alert.MachineName},
		Details: alert.Details, OpenedAt: alert.OpenedAt.UTC(), ResolvedAt: resolvedAt,
		URL: link,
	}
	return body
}

// Les couleurs des embeds et des attachements : celles de la direction
// artistique, en clair.
func color(alert Alert, event Event) (int, string) {
	switch {
	case event == EventResolved:
		return 0x1f9d57, "#1f9d57"
	case event == EventTest:
		return 0x5b5be6, "#5b5be6"
	case alert.Severity == SeverityDanger:
		return 0xde4350, "#de4350"
	}
	return 0xd78d0c, "#d78d0c"
}

// Discord rend le titre cliquable par url, et le lien Markdown en fin de
// description pour qui ne pense pas à cliquer le titre.
func discordPayload(title string, text Text, alert Alert, event Event, now time.Time, link, openLabel string) map[string]any {
	decimal, _ := color(alert, event)
	description := text.Cause
	if text.Action != "" {
		description += "\n" + text.Action
	}
	embed := map[string]any{
		"title": title, "description": description, "color": decimal, "timestamp": now.UTC().Format(time.RFC3339),
	}
	if link != "" {
		embed["url"] = link
		embed["description"] = description + "\n[" + openLabel + "](" + link + ")"
	}
	return map[string]any{"embeds": []map[string]any{embed}}
}

// Slack et Mattermost lisent tous deux title et title_link d'un attachement.
func slackPayload(title string, text Text, alert Alert, event Event, link, openLabel string) map[string]any {
	_, hex := color(alert, event)
	body := text.Cause
	if text.Action != "" {
		body += "\n" + text.Action
	}
	attachment := map[string]any{"color": hex, "text": body}
	if link != "" {
		attachment["title"] = openLabel
		attachment["title_link"] = link
	}
	return map[string]any{
		"text":        title,
		"attachments": []map[string]any{attachment},
	}
}
