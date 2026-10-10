package server

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/mail"
	"github.com/ldesfontaine/opencloud/internal/mail/smtptest"
)

// Le relais s'enregistre sans toucher la langue, le mot de passe ne
// revient jamais, et un mot de passe vide garde celui en place.
func TestAPI_MailSettings_SaveNeverReturnThePasswordAndKeepIt(t *testing.T) {
	server := newTestServer(t)
	if got := callAPI(server.Server, http.MethodPut, "/api/session/language", languageRequest{Language: "en"}); got.Code != http.StatusNoContent {
		t.Fatalf("language: %d", got.Code)
	}
	request := mailSettingsRequest{Host: "smtp.exemple.fr", TLS: "starttls", Username: "ops", Password: "app-password", From: "alertes@exemple.fr"}
	got := callAPI(server.Server, http.MethodPut, "/api/settings/mail", request)
	if got.Code != http.StatusOK || strings.Contains(got.Body.String(), "app-password") {
		t.Fatalf("status %d: %s", got.Code, got.Body.String())
	}
	var saved mailSettingsJSON
	decodeAPI(t, callAPI(server.Server, http.MethodGet, "/api/settings/mail", nil), &saved)
	if !saved.Configured || saved.Port != 587 || !saved.HasPassword || saved.From != "alertes@exemple.fr" {
		t.Fatalf("saved %+v", saved)
	}
	request.Password = ""
	request.Port = 2525
	callAPI(server.Server, http.MethodPut, "/api/settings/mail", request)
	file, err := server.settings.Load()
	if err != nil || file.Language != "en" || file.MailPassword != "app-password" || file.MailPort != 2525 {
		t.Fatalf("file %+v %v", file, err)
	}
	request.ClearPassword = true
	callAPI(server.Server, http.MethodPut, "/api/settings/mail", request)
	if file, _ := server.settings.Load(); file.MailPassword != "" {
		t.Fatalf("password must be cleared: %+v", file)
	}
	callAPI(server.Server, http.MethodPut, "/api/settings/mail", mailSettingsRequest{})
	if file, _ := server.settings.Load(); file.MailHost != "" || file.MailFrom != "" {
		t.Fatalf("an empty host removes the relay: %+v", file)
	}
}

func TestAPI_MailSettings_RefuseWhatMakesNoSense(t *testing.T) {
	server := newTestServer(t)
	cases := map[string]mailSettingsRequest{
		"settings.mail_host_invalid":     {Host: "smtp.exemple.fr:25", TLS: "starttls", From: "a@exemple.fr"},
		"settings.mail_tls_invalid":      {Host: "smtp.exemple.fr", TLS: "ssl", From: "a@exemple.fr"},
		"settings.mail_plain_public":     {Host: "8.8.8.8", TLS: "none", From: "a@exemple.fr"},
		"settings.mail_from_invalid":     {Host: "smtp.exemple.fr", TLS: "starttls", From: "Ops <a@exemple.fr>"},
		"settings.mail_port_invalid":     {Host: "smtp.exemple.fr", Port: 70000, TLS: "starttls", From: "a@exemple.fr"},
		"settings.mail_username_invalid": {Host: "smtp.exemple.fr", TLS: "starttls", Username: "a\r\nb", From: "a@exemple.fr"},
	}
	for want, request := range cases {
		got := callAPI(server.Server, http.MethodPut, "/api/settings/mail", request)
		if got.Code != http.StatusUnprocessableEntity || errorCode(t, got) != want {
			t.Errorf("%s: %d %s", want, got.Code, got.Body.String())
		}
	}
	if file, _ := server.settings.Load(); file.MailHost != "" {
		t.Fatalf("a refused write must not be applied: %+v", file)
	}
}

// L'essai part par le relais enregistré et répond comme le test d'un canal.
func TestAPI_MailSettings_TestSendsThroughTheSavedRelay(t *testing.T) {
	server := newTestServer(t)
	relay := smtptest.Start(t, smtptest.Options{})
	sender := mail.NewSender(5 * time.Second)
	server.notifier.SetMail(server.mailRelay, sender)
	got := callAPI(server.Server, http.MethodPost, "/api/settings/mail/actions/test", mailTestRequest{To: "lucas@exemple.fr"})
	var outcome channelTestResponse
	decodeAPI(t, got, &outcome)
	if outcome.OK || outcome.Reason != "mail_not_configured" {
		t.Fatalf("without relay %+v", outcome)
	}
	request := mailSettingsRequest{Host: relay.Host, Port: relay.Port, TLS: "none", From: "alertes@exemple.fr"}
	if got := callAPI(server.Server, http.MethodPut, "/api/settings/mail", request); got.Code != http.StatusOK {
		t.Fatalf("save %d %s", got.Code, got.Body.String())
	}
	decodeAPI(t, callAPI(server.Server, http.MethodPost, "/api/settings/mail/actions/test", mailTestRequest{To: "lucas@exemple.fr"}), &outcome)
	if !outcome.OK || len(relay.Messages()) != 1 || relay.Messages()[0].Recipients[0] != "lucas@exemple.fr" {
		t.Fatalf("outcome %+v, messages %+v", outcome, relay.Messages())
	}
	refused := callAPI(server.Server, http.MethodPost, "/api/settings/mail/actions/test", mailTestRequest{To: "pas une adresse"})
	if refused.Code != http.StatusUnprocessableEntity || errorCode(t, refused) != "settings.mail_to_invalid" {
		t.Fatalf("bad address %d %s", refused.Code, refused.Body.String())
	}
}

// Un canal e-mail garde ses destinataires et ni URL ni secret ; des
// destinataires invalides sont refusés par une clé du catalogue.
func TestAPI_EmailChannel_KeepsOnlyItsRecipients(t *testing.T) {
	server := newTestServer(t)
	request := channelRequest{Name: "astreinte", URL: "https://x.example", Format: "email", Destination: "a@exemple.fr; b@exemple.fr", Secret: "s", MinSeverity: "danger", Enabled: true}
	got := callAPI(server.Server, http.MethodPost, "/api/alerts/channels", request)
	var created channelJSON
	decodeAPI(t, got, &created)
	if created.Format != "email" || created.URL != "" || created.HasSecret || created.Destination != "a@exemple.fr, b@exemple.fr" {
		t.Fatalf("created %+v", created)
	}
	request.Destination = "a@exemple.fr\r\nBcc: x@y.fr"
	refused := callAPI(server.Server, http.MethodPost, "/api/alerts/channels", request)
	if refused.Code != http.StatusUnprocessableEntity || errorCode(t, refused) != "channel.recipients_invalid" {
		t.Fatalf("refused %d %s", refused.Code, refused.Body.String())
	}
}
