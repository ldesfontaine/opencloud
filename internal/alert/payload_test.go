package alert_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/lang"
)

// Un nom que choisit un agent : un lien masqué qui se ferait passer pour le
// lien d'openCloud, un lien Slack, une mention, une ligne de plus.
const trapped = "web\n[Ouvrir dans openCloud](https://dummy.invalid/x) <https://dummy.invalid/y|Ouvrir> <!channel> @everyone **gras**"

const alertLink = "https://oc.example/alertes/alerte/7"

func renderedAlerts(t *testing.T) map[string]alert.Alert {
	t.Helper()
	return map[string]alert.Alert{
		"service down": {ID: 7, Kind: alert.KindServiceDown, Severity: alert.SeverityDanger, Object: alert.Object{Name: trapped}, MachineName: "vps-1", Details: alert.Details{ExitCode: 1}},
		"disk full":    {ID: 7, Kind: alert.KindDiskFull, Severity: alert.SeverityAttention, Object: alert.Object{Name: "/data"}, MachineName: "vps-1", Details: alert.Details{MountPoint: "/data\n[Ouvrir](https://dummy.invalid/z)", Percent: 90}},
	}
}

func french(t *testing.T) lang.Catalog {
	t.Helper()
	catalogs, err := lang.Load()
	if err != nil {
		t.Fatal(err)
	}
	return catalogs.For(lang.French)
}

// bareSpecials rend les caractères de balisage que rien n'échappe devant eux.
func bareSpecials(text, specials string) []rune {
	var bare []rune
	escaped := false
	for _, r := range text {
		switch {
		case escaped:
			escaped = false
		case r == '\\':
			escaped = true
		case strings.ContainsRune(specials, r):
			bare = append(bare, r)
		}
	}
	return bare
}

func TestRender_DiscordEscapesWhatAnAgentReports(t *testing.T) {
	for name, found := range renderedAlerts(t) {
		body, err := alert.Render(french(t), alert.FormatDiscord, alert.EventOpened, found, testNow, alertLink)
		if err != nil {
			t.Fatal(err)
		}
		var message struct {
			Embeds []struct {
				Description string `json:"description"`
			} `json:"embeds"`
		}
		if err := json.Unmarshal(body.Content, &message); err != nil || len(message.Embeds) != 1 {
			t.Fatalf("%s: %s, %v", name, body.Content, err)
		}
		description := message.Embeds[0].Description
		lines := strings.Split(description, "\n")
		// La cause d'abord, sur une ligne, puis l'action, puis le lien d'openCloud.
		if len(lines) != 3 {
			t.Fatalf("%s: %d lines, want 3:\n%s", name, len(lines), description)
		}
		if bare := bareSpecials(lines[0], "*_~`|<>#[]()@"); len(bare) > 0 {
			t.Errorf("%s: unescaped %q in %q", name, string(bare), lines[0])
		}
		if want := "[Ouvrir dans openCloud](" + alertLink + ")"; lines[2] != want {
			t.Errorf("%s: link line %q, want %q", name, lines[2], want)
		}
	}
}

func TestRender_SlackEncodesWhatAnAgentReports(t *testing.T) {
	for name, found := range renderedAlerts(t) {
		body, err := alert.Render(french(t), alert.FormatSlack, alert.EventOpened, found, testNow, alertLink)
		if err != nil {
			t.Fatal(err)
		}
		var message struct {
			Attachments []struct {
				Text      string `json:"text"`
				TitleLink string `json:"title_link"`
			} `json:"attachments"`
		}
		if err := json.Unmarshal(body.Content, &message); err != nil || len(message.Attachments) != 1 {
			t.Fatalf("%s: %s, %v", name, body.Content, err)
		}
		attachment := message.Attachments[0]
		lines := strings.Split(attachment.Text, "\n")
		if len(lines) != 2 {
			t.Fatalf("%s: %d lines, want cause and action:\n%s", name, len(lines), attachment.Text)
		}
		if strings.ContainsAny(lines[0], "<>") || bareSpecials(lines[0], "[]") != nil {
			t.Errorf("%s: markup left in %q", name, lines[0])
		}
		if attachment.TitleLink != alertLink {
			t.Errorf("%s: title_link %q", name, attachment.TitleLink)
		}
		if name == "service down" && !strings.Contains(lines[0], "&lt;!channel&gt;") {
			t.Errorf("%s: <!channel> should be encoded: %q", name, lines[0])
		}
	}
}

// Un nom ordinaire passe tel quel, et la phrase du catalogue n'est jamais
// touchée (ici ses parenthèses) : seuls les noms que choisit l'agent le sont.
func TestRender_AnOrdinaryNameIsUntouched(t *testing.T) {
	catalog := french(t)
	found := alert.Alert{ID: 7, Kind: alert.KindProbeDown, Severity: alert.SeverityDanger, Object: alert.Object{Name: "nextcloud-web"}, MachineName: "vps-1", Details: alert.Details{Target: "cloud.exemple.fr", Reason: "refused"}}
	want := alert.Describe(catalog, found, testNow).Cause
	if !strings.Contains(want, "(") {
		t.Fatalf("the cause %q should carry the catalog's parentheses", want)
	}
	for _, format := range []alert.Format{alert.FormatDiscord, alert.FormatSlack} {
		body, err := alert.Render(catalog, format, alert.EventOpened, found, testNow, alertLink)
		if err != nil {
			t.Fatal(err)
		}
		var message struct {
			Embeds []struct {
				Description string `json:"description"`
			} `json:"embeds"`
			Attachments []struct {
				Text string `json:"text"`
			} `json:"attachments"`
		}
		if err := json.Unmarshal(body.Content, &message); err != nil {
			t.Fatal(err)
		}
		got := ""
		if format == alert.FormatDiscord {
			got = message.Embeds[0].Description
		} else {
			got = message.Attachments[0].Text
		}
		if !strings.HasPrefix(got, want+"\n") {
			t.Errorf("%s: %q does not start with %q", format, got, want)
		}
	}
}

// Le résumé des alertes retenues nomme des machines : leurs noms passent par
// le même échappement que les alertes, dans le même balisage.
func TestRenderSummary_EscapesMachineNames(t *testing.T) {
	held := alert.Held{Count: 3, Severity: alert.SeverityDanger, Machines: []string{trapped, "vps-2"}, Others: 1}
	for _, format := range []alert.Format{alert.FormatDiscord, alert.FormatSlack} {
		body, err := alert.RenderSummary(french(t), format, held, testNow, alertLink)
		if err != nil {
			t.Fatal(err)
		}
		content := string(body.Content)
		if strings.Contains(content, "[Ouvrir dans openCloud](https://dummy.invalid/x)") || strings.Contains(content, "<!channel>") {
			t.Errorf("%s: a machine name reached the summary unescaped:\n%s", format, content)
		}
		if !strings.Contains(content, "vps-2") {
			t.Errorf("%s: an ordinary machine name is missing:\n%s", format, content)
		}
	}
}
