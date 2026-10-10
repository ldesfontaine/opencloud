package alert_test

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/mail"
	"github.com/ldesfontaine/opencloud/internal/mail/smtptest"
)

// mailNotifier branche le notifieur sur un faux relais en STARTTLS.
func (f *fixture) mailNotifier(t *testing.T, relay *smtptest.Server, password string) *alert.Notifier {
	t.Helper()
	notifier := f.notifier(t, lang.French)
	sender := mail.NewSender(5 * time.Second)
	sender.SetRootCAs(relay.RootCAs)
	settings := mail.Relay{
		Host: relay.Host, Port: relay.Port, TLS: mail.TLSStartTLS, Username: "ops", Password: password, From: "alertes@exemple.fr",
	}
	notifier.SetMail(func() mail.Relay { return settings }, sender)
	return notifier
}

func (f *fixture) emailChannel(t *testing.T, destination string) alert.Channel {
	t.Helper()
	channel, err := f.CreateChannel(context.Background(), alert.ChannelDefinition{
		Name: "astreinte", URL: "https://ignored.example", Format: alert.FormatEmail, Destination: destination, Secret: "ignored",
		MinSeverity: alert.SeverityAttention, NotifyResolve: true, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return channel
}

func TestNotifier_EmailChannel_DeliversTheAlertTextWithItsLink(t *testing.T) {
	f := newFixture(t)
	relay := smtptest.Start(t, smtptest.Options{Username: "ops", Password: "app-password", StartTLS: true})
	notifier := f.mailNotifier(t, relay, "app-password")
	notifier.SetPublicURL("https://oc.example.fr")
	channel := f.emailChannel(t, "lucas@exemple.fr; astreinte@exemple.fr, lucas@exemple.fr")
	if channel.URL != "" || channel.Secret != "" || channel.Destination != "lucas@exemple.fr, astreinte@exemple.fr" {
		t.Fatalf("an email channel keeps only its recipients: %+v", channel)
	}
	f.Open(context.Background(), volumeFact(alert.SeverityAttention, 86))
	opened := f.only(t, alert.StatusOpen)
	delivery := f.waitDelivery(t, opened.ID)
	if delivery.Status != alert.DeliveryDelivered || delivery.Attempts != 1 {
		t.Fatalf("delivery %+v", delivery)
	}
	messages := relay.Messages()
	if len(messages) != 1 || !messages[0].Secured || strings.Join(messages[0].Recipients, ",") != "lucas@exemple.fr,astreinte@exemple.fr" {
		t.Fatalf("messages %+v", messages)
	}
	data := messages[0].Data
	subjectLine := data[strings.Index(data, "Subject: ")+len("Subject: "):]
	subject, err := new(mime.WordDecoder).DecodeHeader(subjectLine[:strings.Index(subjectLine, "\r\n")])
	if err != nil || subject != "Alerte · Disque à 86 %" {
		t.Fatalf("subject %q, %v", subject, err)
	}
	if !strings.Contains(data, "/alertes/alerte/") || !strings.Contains(data, "Auto-Submitted: auto-generated") || !strings.Contains(data, "text/html") {
		t.Fatalf("body %s", data)
	}
}

func TestNotifier_EmailChannel_AuthenticationRefusalIsAReadableReason(t *testing.T) {
	f := newFixture(t)
	relay := smtptest.Start(t, smtptest.Options{Username: "ops", Password: "app-password", StartTLS: true})
	notifier := f.mailNotifier(t, relay, "wrong")
	channel := f.emailChannel(t, "lucas@exemple.fr")
	outcome, err := notifier.Test(context.Background(), channel)
	if err != nil || outcome.Reason != alert.ReasonSMTPAuthFailed || outcome.Code != 535 {
		t.Fatalf("outcome %+v, %v", outcome, err)
	}
	f.Open(context.Background(), volumeFact(alert.SeverityAttention, 86))
	delivery := f.waitDelivery(t, f.only(t, alert.StatusOpen).ID)
	if delivery.Status != alert.DeliveryFailed || delivery.Attempts != alert.MaxAttempts || delivery.Reason != alert.ReasonSMTPAuthFailed {
		t.Fatalf("delivery %+v", delivery)
	}
}

func TestNotifier_EmailChannel_WithoutARelay_SaysSo(t *testing.T) {
	f := newFixture(t)
	notifier := f.notifier(t, lang.French)
	outcome, err := notifier.Test(context.Background(), alert.Channel{Format: alert.FormatEmail, Destination: "a@exemple.fr"})
	if err != nil || outcome.Reason != alert.ReasonMailNotConfigured {
		t.Fatalf("outcome %+v, %v", outcome, err)
	}
	notifier.SetMail(func() mail.Relay { return mail.Relay{} }, mail.NewSender(time.Second))
	if outcome, _ := notifier.TestMail(context.Background(), "a@exemple.fr"); outcome.Reason != alert.ReasonMailNotConfigured {
		t.Fatalf("empty relay: %+v", outcome)
	}
}

func TestChannelDefinition_Email_RefusesBadRecipients(t *testing.T) {
	eleven := []string{}
	for index := range 11 {
		eleven = append(eleven, fmt.Sprintf("ops%d@exemple.fr", index))
	}
	for _, destination := range []string{"", "pas une adresse", "a@exemple.fr\r\nBcc: x@y.fr", strings.Join(eleven, ",")} {
		definition := alert.ChannelDefinition{Name: "x", Format: alert.FormatEmail, Destination: destination, MinSeverity: alert.SeverityDanger}
		if err := definition.Validate(); !errors.Is(err, alert.ErrChannelRecipients) {
			t.Errorf("%q: %v", destination, err)
		}
	}
}
