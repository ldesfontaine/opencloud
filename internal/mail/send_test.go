package mail_test

import (
	"context"
	"errors"
	"mime"
	"strings"
	"testing"
	"time"

	"github.com/ldesfontaine/opencloud/internal/mail"
	"github.com/ldesfontaine/opencloud/internal/mail/smtptest"
)

func relayFor(server *smtptest.Server, mode mail.TLSMode) mail.Relay {
	return mail.Relay{
		Host: server.Host, Port: server.Port, TLS: mode,
		Username: "ops", Password: "app-password", From: "alertes@exemple.fr",
	}
}

func newSender(server *smtptest.Server) *mail.Sender {
	sender := mail.NewSender(5 * time.Second)
	sender.SetRootCAs(server.RootCAs)
	return sender
}

var accented = mail.Message{
	To:      []string{"lucas@exemple.fr", "astreinte@exemple.fr"},
	Subject: "Alerte · Tâche en échec",
	Text:    "La tâche sauvegarde a échoué, code 2.\nRelancez-la.",
	HTML:    "<p>La tâche sauvegarde a échoué.</p>",
}

func TestSender_StartTLS_AuthenticatesAndDeliversAnEncodedSubject(t *testing.T) {
	server := smtptest.Start(t, smtptest.Options{Username: "ops", Password: "app-password", StartTLS: true})
	if err := newSender(server).Send(context.Background(), relayFor(server, mail.TLSStartTLS), accented); err != nil {
		t.Fatal(err)
	}
	messages := server.Messages()
	if len(messages) != 1 {
		t.Fatalf("messages %d", len(messages))
	}
	got := messages[0]
	if !got.Secured || got.Auth != "ops" || got.From != "alertes@exemple.fr" || strings.Join(got.Recipients, ",") != "lucas@exemple.fr,astreinte@exemple.fr" {
		t.Fatalf("envelope %+v", got)
	}
	headers := headerMap(got.Data)
	subject, err := new(mime.WordDecoder).DecodeHeader(headers["Subject"])
	if err != nil || subject != accented.Subject || !strings.HasPrefix(headers["Subject"], "=?utf-8?q?") {
		t.Fatalf("subject %q decoded %q, %v", headers["Subject"], subject, err)
	}
	if headers["Auto-Submitted"] != "auto-generated" || !strings.HasSuffix(headers["Message-ID"], "@exemple.fr>") || headers["Date"] == "" {
		t.Fatalf("headers %v", headers)
	}
	if !strings.Contains(got.Data, "La t=C3=A2che sauvegarde a =C3=A9chou=C3=A9") || !strings.Contains(got.Data, "text/html") {
		t.Fatalf("body %s", got.Data)
	}
}

func TestSender_ImplicitTLS_Delivers(t *testing.T) {
	server := smtptest.Start(t, smtptest.Options{Username: "ops", Password: "app-password", Implicit: true})
	if err := newSender(server).Send(context.Background(), relayFor(server, mail.TLSImplicit), accented); err != nil {
		t.Fatal(err)
	}
	if messages := server.Messages(); len(messages) != 1 || !messages[0].Secured {
		t.Fatalf("messages %+v", messages)
	}
}

func TestSender_WrongPassword_IsAnAuthenticationRefusal(t *testing.T) {
	server := smtptest.Start(t, smtptest.Options{Username: "ops", Password: "app-password", StartTLS: true})
	relay := relayFor(server, mail.TLSStartTLS)
	relay.Password = "wrong"
	err := newSender(server).Send(context.Background(), relay, accented)
	var relayErr *mail.RelayError
	if !errors.Is(err, mail.ErrAuthFailed) || !errors.As(err, &relayErr) || relayErr.Code != 535 {
		t.Fatalf("error %v", err)
	}
	if len(server.Messages()) != 0 {
		t.Fatal("nothing must be delivered")
	}
}

func TestSender_RefusedRecipient_IsARejection(t *testing.T) {
	server := smtptest.Start(t, smtptest.Options{Username: "ops", Password: "app-password", StartTLS: true, RejectRecipient: "astreinte@exemple.fr"})
	err := newSender(server).Send(context.Background(), relayFor(server, mail.TLSStartTLS), accented)
	if !errors.Is(err, mail.ErrRejected) {
		t.Fatalf("error %v", err)
	}
}

// STARTTLS demandé à un relais qui ne le propose pas : on ne retombe
// jamais en clair, le mot de passe ne part pas.
func TestSender_StartTLSMissing_RefusesToSendInClear(t *testing.T) {
	server := smtptest.Start(t, smtptest.Options{Username: "ops", Password: "app-password"})
	err := newSender(server).Send(context.Background(), relayFor(server, mail.TLSStartTLS), accented)
	if !errors.Is(err, mail.ErrTLS) || len(server.Messages()) != 0 {
		t.Fatalf("error %v", err)
	}
}

func TestSender_UnknownCertificate_IsATLSFailure(t *testing.T) {
	server := smtptest.Start(t, smtptest.Options{StartTLS: true})
	relay := relayFor(server, mail.TLSStartTLS)
	relay.Username = ""
	err := mail.NewSender(5*time.Second).Send(context.Background(), relay, accented)
	if !errors.Is(err, mail.ErrTLS) {
		t.Fatalf("error %v", err)
	}
}

func TestSender_PlainRelayOnLoopback_DeliversWithoutTLS(t *testing.T) {
	server := smtptest.Start(t, smtptest.Options{})
	relay := relayFor(server, mail.TLSNone)
	relay.Username = ""
	if err := newSender(server).Send(context.Background(), relay, mail.Message{To: []string{"a@exemple.fr"}, Subject: "Test", Text: "ok"}); err != nil {
		t.Fatal(err)
	}
	if messages := server.Messages(); len(messages) != 1 || messages[0].Secured {
		t.Fatalf("messages %+v", messages)
	}
}

func TestSender_LinkLocalRelay_IsForbidden(t *testing.T) {
	relay := mail.Relay{Host: "169.254.169.254", Port: 25, TLS: mail.TLSStartTLS, From: "a@exemple.fr"}
	err := mail.NewSender(time.Second).Send(context.Background(), relay, mail.Message{To: []string{"a@exemple.fr"}})
	if err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("error %v", err)
	}
}

func TestRelay_Validate_RefusesWhatMakesNoSense(t *testing.T) {
	good := mail.Relay{Host: "smtp.exemple.fr", Port: 587, TLS: mail.TLSStartTLS, Username: "u", Password: "p", From: "a@exemple.fr"}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		change func(*mail.Relay)
		want   error
	}{
		"host with a port":   {func(r *mail.Relay) { r.Host = "smtp.exemple.fr:25" }, mail.ErrHostInvalid},
		"port zero":          {func(r *mail.Relay) { r.Port = 0 }, mail.ErrPortInvalid},
		"unknown tls":        {func(r *mail.Relay) { r.TLS = "ssl" }, mail.ErrTLSModeInvalid},
		"plain to public ip": {func(r *mail.Relay) { r.Host = "8.8.8.8"; r.TLS = mail.TLSNone }, mail.ErrPlainNotPrivate},
		"sender with name":   {func(r *mail.Relay) { r.From = "Ops <a@exemple.fr>" }, mail.ErrFromInvalid},
		"header injection":   {func(r *mail.Relay) { r.Username = "u\r\nRCPT TO:<x@y.z>" }, mail.ErrUsernameInvalid},
	}
	for name, test := range cases {
		relay := good
		test.change(&relay)
		if err := relay.Validate(); !errors.Is(err, test.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	plainPrivate := good
	plainPrivate.Host, plainPrivate.TLS = "192.168.1.10", mail.TLSNone
	if err := plainPrivate.Validate(); err != nil {
		t.Fatalf("plain on a private network: %v", err)
	}
}

func TestValidateRecipients_BoundsAndRefusesInjection(t *testing.T) {
	if err := mail.ValidateRecipients(nil); !errors.Is(err, mail.ErrNoRecipient) {
		t.Fatal(err)
	}
	many := make([]string, mail.MaxRecipients+1)
	for index := range many {
		many[index] = "a@exemple.fr"
	}
	if err := mail.ValidateRecipients(many); !errors.Is(err, mail.ErrTooManyAddress) {
		t.Fatal(err)
	}
	for _, bad := range []string{"a@exemple.fr\r\nBcc: x@y.z", "a@localhost", "pas une adresse", "a@b.fr, c@d.fr"} {
		if err := mail.ValidateRecipients([]string{bad}); !errors.Is(err, mail.ErrAddressInvalid) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func headerMap(data string) map[string]string {
	headers := map[string]string{}
	head, _, _ := strings.Cut(data, "\r\n\r\n")
	for _, line := range strings.Split(head, "\r\n") {
		name, value, found := strings.Cut(line, ": ")
		if found {
			headers[name] = value
		}
	}
	return headers
}
