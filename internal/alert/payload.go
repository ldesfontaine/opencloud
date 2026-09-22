package alert

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/ldesfontaine/opencloud/internal/lang"
)

// Body est ce qu'un canal reçoit : le corps, son type, et le titre que
// certains récepteurs lisent en en-tête.
type Body struct {
	Content     []byte
	ContentType string
	Title       string
}

// Render fait le corps d'un événement pour un format. Le titre porte
// l'événement : « Alerte », « Alerte aggravée », « Résolu », « Test ».
func Render(catalog lang.Catalog, format Format, event Event, alert Alert, now time.Time) (Body, error) {
	text := Describe(catalog, alert, now)
	if event == EventTest {
		text = Text{Fact: catalog.Get("alert.test_fact"), Cause: catalog.Get("alert.test_cause"), Action: ""}
	}
	title := catalog.Get("alert.notify_"+string(event)) + " · " + text.Fact
	switch format {
	case FormatText:
		return Body{Content: []byte(plainText(title, text)), ContentType: "text/plain; charset=utf-8", Title: title}, nil
	case FormatDiscord:
		return jsonBody(title, discordPayload(title, text, alert, event, now))
	case FormatSlack:
		return jsonBody(title, slackPayload(title, text, alert, event))
	}
	return jsonBody(title, openCloudPayload(catalog, title, text, alert, event, now))
}

func jsonBody(title string, payload any) (Body, error) {
	content, err := json.Marshal(payload)
	if err != nil {
		return Body{}, err
	}
	return Body{Content: content, ContentType: "application/json", Title: title}, nil
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
	// Alert manque au corps d'un test : il n'y a rien en alerte.
	Alert *payloadAlert `json:"alert,omitempty"`
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
}

type payloadObject struct {
	Kind string `json:"kind,omitempty"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

func openCloudPayload(catalog lang.Catalog, title string, text Text, alert Alert, event Event, now time.Time) payload {
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

func discordPayload(title string, text Text, alert Alert, event Event, now time.Time) map[string]any {
	decimal, _ := color(alert, event)
	description := text.Cause
	if text.Action != "" {
		description += "\n" + text.Action
	}
	return map[string]any{
		"embeds": []map[string]any{{
			"title": title, "description": description, "color": decimal, "timestamp": now.UTC().Format(time.RFC3339),
		}},
	}
}

func slackPayload(title string, text Text, alert Alert, event Event) map[string]any {
	_, hex := color(alert, event)
	body := text.Cause
	if text.Action != "" {
		body += "\n" + text.Action
	}
	return map[string]any{
		"text":        title,
		"attachments": []map[string]any{{"color": hex, "text": body}},
	}
}
