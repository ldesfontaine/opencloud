package alert

import (
	"encoding/json"
	"html"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/ldesfontaine/opencloud/internal/lang"
)

// Body est ce qu'un canal reçoit : le corps, son type, le titre et le
// lien que certains récepteurs lisent en en-tête. HTML est la variante
// d'un e-mail, à côté du texte brut ; TransactionID nomme l'envoi chez
// Matrix.
type Body struct {
	Content       []byte
	ContentType   string
	Title         string
	Link          string
	HTML          string
	TransactionID string
	// Urgent : le récepteur qui sait classer par priorité (ntfy) doit le
	// lire tout de suite. Seul un résumé le pose : une alerte part toujours
	// en priorité haute.
	Urgent bool
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
// d'un canal a retenu : « N alertes de plus, voir openCloud », avec les
// machines concernées, et rendu à la gravité la plus haute des alertes
// retenues.
func RenderSummary(catalog lang.Catalog, format Format, held Held, now time.Time, link string) (Body, error) {
	text := Text{Fact: catalog.Format("alert.summary_fact", held.Count), Cause: summaryCause(catalog, held, escaperOf(format))}
	title := catalog.Get("alert.notify_summary") + " · " + text.Fact
	openLabel := catalog.Get("alert.open_in_opencloud")
	alert := Alert{Severity: held.Severity}
	var rendered Body
	var err error
	switch format {
	case FormatText:
		rendered = Body{Content: []byte(plainText(title, text)), ContentType: "text/plain; charset=utf-8", Title: title, Link: link}
	case FormatDiscord:
		rendered, err = jsonBody(title, link, discordPayload(title, text, alert, EventSummary, now, link, openLabel))
	case FormatSlack:
		rendered, err = jsonBody(title, link, slackPayload(title, text, alert, EventSummary, link, openLabel))
	default:
		rendered, err = jsonBody(title, link, payload{
			Event: string(EventSummary), SentAt: now.UTC(), Language: string(catalog.Code()), Title: title,
			Text: payloadText(text), Held: held.Count, Severity: held.Severity, Machines: held.Machines, OtherMachines: held.Others, URL: link,
		})
	}
	rendered.Urgent = held.Severity == SeverityDanger
	return rendered, err
}

// summaryCause dit pourquoi le résumé existe, puis ce qui compte à qui le
// lit : si le danger est dedans, et quelles machines.
func summaryCause(catalog lang.Catalog, held Held, escape func(string) string) string {
	if escape == nil {
		escape = func(text string) string { return text }
	}
	sentences := []string{catalog.Get("alert.summary_cause")}
	if held.Severity == SeverityDanger {
		sentences = append(sentences, catalog.Get("alert.summary_danger"))
	}
	if len(held.Machines) > 0 {
		// Les noms de machines viennent de l'opérateur, mais le résumé part
		// dans le même balisage que les alertes : même échappement.
		names := make([]string, 0, len(held.Machines))
		for _, machine := range held.Machines {
			names = append(names, escape(machine))
		}
		joined := strings.Join(names, ", ")
		if held.Others > 0 {
			sentences = append(sentences, catalog.Format("alert.summary_machines_more", joined, held.Others))
		} else {
			sentences = append(sentences, catalog.Format("alert.summary_machines", joined))
		}
	}
	return strings.Join(sentences, " ")
}

// Render fait le corps d'un événement pour un format. Le titre porte
// l'événement : « Alerte », « Alerte aggravée », « Résolu », « Test ».
// Le lien, quand il y en a un, mène à la fiche de l'alerte.
func Render(catalog lang.Catalog, format Format, event Event, alert Alert, now time.Time, link string) (Body, error) {
	text := describe(catalog, alert, now, escaperOf(format))
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
	case FormatEmail:
		return emailBody(title, text, link, openLabel), nil
	case FormatMatrix:
		return matrixBody(title, text, link, openLabel)
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

// emailBody fait le texte brut, le lien en dernière ligne, et un HTML
// sans style ni image : un client de messagerie l'affiche tel quel.
func emailBody(title string, text Text, link, openLabel string) Body {
	plain := plainText(title, text)
	paragraphs := []string{"<p><strong>" + html.EscapeString(title) + "</strong></p>", "<p>" + html.EscapeString(text.Cause) + "</p>"}
	if text.Action != "" {
		paragraphs = append(paragraphs, "<p>"+html.EscapeString(text.Action)+"</p>")
	}
	if link != "" {
		plain += "\n\n" + openLabel + " : " + link
		paragraphs = append(paragraphs, `<p><a href="`+html.EscapeString(link)+`">`+html.EscapeString(openLabel)+"</a></p>")
	}
	return Body{
		Content: []byte(plain), ContentType: "text/plain; charset=utf-8", Title: title, Link: link,
		HTML: "<!doctype html><html><body>" + strings.Join(paragraphs, "") + "</body></html>",
	}
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
	// Held : ce qu'un résumé remplace ; Severity, Machines et OtherMachines
	// disent la gravité la plus haute retenue et les machines concernées ;
	// URL : la page des alertes.
	Held          int      `json:"held,omitempty"`
	Severity      Severity `json:"severity,omitempty"`
	Machines      []string `json:"machines,omitempty"`
	OtherMachines int      `json:"other_machines,omitempty"`
	URL           string   `json:"url,omitempty"`
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

// escaperOf dit comment un format protège le texte que choisit un agent
// (noms, points de montage). Discord lit du Markdown, Slack des chevrons ;
// le mail et Matrix échappent déjà le HTML à l'écriture, le texte brut et le
// JSON ne sont lus par aucun balisage.
func escaperOf(format Format) func(string) string {
	switch format {
	case FormatDiscord:
		return escapeDiscord
	case FormatSlack:
		return escapeSlack
	}
	return nil
}

// flatten remplace tout saut de ligne ou caractère de contrôle par une
// espace : un nom ne doit pas faire une ligne de plus dans le message.
func flatten(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return ' '
		}
		return r
	}, text)
}

// markdownSpecials est ce que Discord lit comme du Markdown, comme un lien
// (« <https://…> ») ou comme une mention (« <@…> », « @everyone ») : chacun
// reçoit une barre oblique inverse.
const markdownSpecials = "\\*_~`|<>#[]()@"

// escapeDiscord rend un texte inerte dans une description d'embed : ni lien
// masqué, ni mise en forme, ni mention, sur une seule ligne.
func escapeDiscord(text string) string {
	var out strings.Builder
	for _, r := range flatten(text) {
		if strings.ContainsRune(markdownSpecials, r) {
			out.WriteByte('\\')
		}
		out.WriteRune(r)
	}
	return out.String()
}

// escapeSlack encode ce que Slack lit comme un lien, une mention ou une
// entité : « <https://…|texte> », « <!channel> ». Mattermost lit le même
// message en Markdown : la barre oblique inverse et les crochets, qui font
// ses liens masqués, sont protégés aussi.
func escapeSlack(text string) string {
	return slackEscaper.Replace(flatten(text))
}

var slackEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\\", "\\\\", "[", "\\[", "]", "\\]")

// Discord rend le titre cliquable par url, et le lien Markdown en fin de
// description pour qui ne pense pas à cliquer le titre. Le texte arrive déjà
// protégé par escapeDiscord : le seul lien actif est celui d'openCloud.
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
// Le texte arrive déjà protégé par escapeSlack.
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
