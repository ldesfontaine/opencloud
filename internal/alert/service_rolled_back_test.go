package alert_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/machine"
)

// declareService déclare un service sur la machine openCloud : l'alerte d'une
// montée y est rattachée (service_id est une clé étrangère).
func (f *fixture) declareService(t *testing.T, name string) directory.Service {
	t.Helper()
	declared := directory.New(f.db, discardLogger())
	created, err := declared.CreateService(context.Background(), directory.ServiceDefinition{
		MachineID: machine.LocalID, Name: name, Nature: directory.NatureApplication, Origin: directory.OriginCompose,
		ComposeProject: name, Units: []directory.UnitDefinition{{Name: "app", Image: "app", Version: "1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return created
}

var rollbackJournal = []string{"app | connection refused", "app | exiting with code 1"}

// Une montée revenue en arrière ouvre l'alerte du service ; danger quand le
// retour a échoué aussi ; chaque service a la sienne ; une montée confirmée la
// referme.
func TestServiceRolledBack_OpensPerServiceAndAConfirmedUpdateResolvesIt(t *testing.T) {
	f := newFixture(t)
	f.channel(t, "ops", alert.SeverityAttention, true)
	ctx := context.Background()

	first, second := f.declareService(t, "nextcloud").ID, f.declareService(t, "wiki").ID
	f.ServiceRolledBack(ctx, first, "Nextcloud", machine.LocalID, "orca", "29.0.4", "29.0.5", false, false, rollbackJournal)
	f.ServiceRolledBack(ctx, second, "Wiki", machine.LocalID, "orca", "1.0", "1.1", false, false, nil)
	listed, err := f.db.ListAlerts(ctx, alert.Filter{Status: alert.StatusOpen, Kind: alert.KindServiceRolledBack})
	if err != nil || len(listed) != 2 {
		t.Fatalf("alerts %+v %v", listed, err)
	}
	var nextcloud alert.Alert
	for _, found := range listed {
		if found.Object.ID == first {
			nextcloud = found
		}
	}
	if nextcloud.Object.Kind != alert.ObjectService || nextcloud.ServiceID != first || nextcloud.Severity != alert.SeverityAttention ||
		nextcloud.Details.Target != "29.0.5" || nextcloud.Details.Version != "29.0.4" || strings.Join(nextcloud.Details.Log, "|") != strings.Join(rollbackJournal, "|") {
		t.Fatalf("alert %+v", nextcloud)
	}

	// Le retour échoue à son tour : la même alerte s'aggrave, elle ne se double pas.
	f.ServiceRolledBack(ctx, first, "Nextcloud", machine.LocalID, "orca", "29.0.4", "29.0.5", true, false, rollbackJournal)
	listed, _ = f.db.ListAlerts(ctx, alert.Filter{Status: alert.StatusOpen, Kind: alert.KindServiceRolledBack})
	dangers := 0
	for _, found := range listed {
		if found.Severity == alert.SeverityDanger {
			dangers++
		}
	}
	if len(listed) != 2 || dangers != 1 {
		t.Fatalf("%d alerts, %d in danger", len(listed), dangers)
	}

	f.ServiceUpdated(ctx, first)
	listed, _ = f.db.ListAlerts(ctx, alert.Filter{Status: alert.StatusOpen, Kind: alert.KindServiceRolledBack})
	if len(listed) != 1 || listed[0].Object.ID != second {
		t.Fatalf("a confirmed update must close its own alert only: %+v", listed)
	}
}

func TestServiceRolledBack_IsSaidInBothLanguagesWithTheVersions(t *testing.T) {
	catalogs, err := lang.Load()
	if err != nil {
		t.Fatal(err)
	}
	base := alert.Alert{
		Kind: alert.KindServiceRolledBack, Severity: alert.SeverityAttention, MachineName: "orca",
		Object:  alert.Object{Kind: alert.ObjectService, ID: "svc_1", Name: "Nextcloud"},
		Details: alert.Details{Reason: alert.ReasonRolledBack, Target: "29.0.5", Version: "29.0.4", Log: rollbackJournal},
	}
	failed := base
	failed.Details.Reason = alert.ReasonRollbackFailed

	for _, code := range []lang.Code{lang.French, lang.English} {
		rolled := alert.Describe(catalogs.For(code), base, testNow)
		bad := alert.Describe(catalogs.For(code), failed, testNow)
		for _, text := range []alert.Text{rolled, bad} {
			if text.Fact == "" || text.Action == "" || strings.Contains(text.Cause, "%!") || strings.Contains(text.Cause, "[") ||
				!strings.Contains(text.Cause, "Nextcloud") || !strings.Contains(text.Cause, "29.0.5") || !strings.Contains(text.Cause, "29.0.4") {
				t.Errorf("%s: text %+v", code, text)
			}
			if len(text.Logs) != len(rollbackJournal) || text.LogsLabel == "" {
				t.Errorf("%s: the journal is not carried: %+v", code, text)
			}
		}
		if rolled.Fact == bad.Fact || rolled.Cause == bad.Cause {
			t.Errorf("%s: a failed return reads like a rolled back one", code)
		}
	}
	// Sans journal, rien à joindre.
	base.Details.Log = nil
	if text := alert.Describe(catalogs.For(lang.French), base, testNow); len(text.Logs) != 0 || text.LogsLabel != "" {
		t.Errorf("journal label without journal: %+v", text)
	}
}

// Le journal se lit dans chaque format, protégé à sa façon ; un journal qui
// porte une clôture Markdown ne referme pas le bloc.
func TestRender_ServiceRolledBackCarriesTheJournalInEveryFormat(t *testing.T) {
	found := alert.Alert{
		ID: 7, Kind: alert.KindServiceRolledBack, Severity: alert.SeverityAttention, MachineName: "orca",
		Object:  alert.Object{Kind: alert.ObjectService, ID: "svc_1", Name: "Nextcloud"},
		Details: alert.Details{Reason: alert.ReasonRolledBack, Target: "29.0.5", Version: "29.0.4", Log: []string{"app | <b>boom</b> & ```fence```", "app | exiting"}},
	}
	for _, format := range alert.Formats() {
		body, err := alert.Render(french(t), format, alert.EventOpened, found, testNow, alertLink)
		if err != nil {
			t.Fatal(err)
		}
		content := string(body.Content)
		if !strings.Contains(content, "exiting") {
			t.Errorf("%s: the journal is missing:\n%s", format, content)
		}
		switch format {
		case alert.FormatDiscord:
			var message struct {
				Embeds []struct {
					Description string `json:"description"`
				} `json:"embeds"`
			}
			if err := json.Unmarshal(body.Content, &message); err != nil {
				t.Fatal(err)
			}
			description := message.Embeds[0].Description
			if strings.Count(description, "```") != 2 {
				t.Errorf("discord: the journal closed its own block:\n%s", description)
			}
		case alert.FormatSlack:
			var message struct {
				Attachments []struct {
					Text string `json:"text"`
				} `json:"attachments"`
			}
			if err := json.Unmarshal(body.Content, &message); err != nil {
				t.Fatal(err)
			}
			if text := message.Attachments[0].Text; strings.Contains(text, "<b>") || !strings.Contains(text, "&lt;b&gt;") {
				t.Errorf("slack: the markup was not encoded:\n%s", text)
			}
		case alert.FormatEmail:
			if strings.Contains(body.HTML, "<b>boom") || !strings.Contains(body.HTML, "&lt;b&gt;boom") {
				t.Errorf("email: the html was not escaped:\n%s", body.HTML)
			}
		}
	}
}

// Le journal ne quitte la maison que par un canal qui le veut : la case coupe
// la livraison, et le navigateur le garde dans l'alerte.
func TestNotifier_AChannelThatTurnedLogsOffNeverSendsThem(t *testing.T) {
	for _, attach := range []bool{true, false} {
		f := newFixture(t)
		receiverServer := newReceiver(200)
		defer receiverServer.Close()
		if _, err := f.CreateChannel(context.Background(), alert.ChannelDefinition{
			Name: "ops", URL: receiverServer.URL, Format: alert.FormatText, MinSeverity: alert.SeverityAttention, NotifyResolve: true, AttachLogs: attach, Enabled: true,
		}); err != nil {
			t.Fatal(err)
		}
		f.notifier(t, lang.French)

		f.ServiceRolledBack(context.Background(), f.declareService(t, "nextcloud").ID, "Nextcloud", machine.LocalID, "orca", "29.0.4", "29.0.5", false, false, rollbackJournal)
		opened := f.only(t, alert.StatusOpen)
		f.waitDelivery(t, opened.ID)

		sent := string(receiverServer.last().body)
		if got := strings.Contains(sent, "connection refused"); got != attach {
			t.Errorf("attach_logs=%v: journal in the message=%v\n%s", attach, got, sent)
		}
		if !strings.Contains(sent, "Nextcloud") {
			t.Errorf("attach_logs=%v: the message lost its text:\n%s", attach, sent)
		}
		if len(opened.Details.Log) != 2 {
			t.Errorf("the alert itself must keep the journal: %+v", opened.Details)
		}
	}
}
