package alert

import (
	"context"
	"errors"

	"github.com/ldesfontaine/opencloud/internal/mail"
)

// Ce que le notifieur attend de l'envoi d'e-mails : livrer un message par
// un relais.
type MailSender interface {
	Send(ctx context.Context, relay mail.Relay, message mail.Message) error
}

// SetMail branche l'e-mail : relay relit le relais réglé à chaque envoi,
// un changement sous Paramètres vaut donc dès l'alerte suivante.
func (n *Notifier) SetMail(relay func() mail.Relay, sender MailSender) {
	n.mailRelay = relay
	n.mailSender = sender
}

// sendMail livre le corps d'un canal e-mail à ses destinataires.
func (n *Notifier) sendMail(ctx context.Context, channel Channel, body Body) Outcome {
	if n.mailSender == nil || n.mailRelay == nil {
		return Outcome{Reason: ReasonMailNotConfigured}
	}
	message := mail.Message{
		To: Recipients(channel.Destination), Subject: body.Title, Text: string(body.Content), HTML: body.HTML,
	}
	return mailOutcome(n.mailSender.Send(ctx, n.mailRelay(), message))
}

// TestMail envoie l'e-mail de test des Paramètres, par le relais réglé,
// tout de suite et sans essai de plus.
func (n *Notifier) TestMail(ctx context.Context, recipient string) (Outcome, error) {
	channel := Channel{Format: FormatEmail, Destination: recipient}
	body, err := n.render(FormatEmail, EventTest, Alert{Severity: SeverityAttention})
	if err != nil {
		return Outcome{}, err
	}
	return n.sendMail(ctx, channel, body), nil
}

// mailOutcome traduit la réponse du relais en un mot de la liste fermée,
// avec le code SMTP quand il y en a un.
func mailOutcome(err error) Outcome {
	if err == nil {
		return Outcome{}
	}
	code := 0
	var relayErr *mail.RelayError
	if errors.As(err, &relayErr) {
		code = relayErr.Code
	}
	switch {
	case errors.Is(err, mail.ErrNotConfigured):
		return Outcome{Reason: ReasonMailNotConfigured}
	case errors.Is(err, mail.ErrAuthFailed):
		return Outcome{Reason: ReasonSMTPAuthFailed, Code: code}
	case errors.Is(err, mail.ErrRejected):
		return Outcome{Reason: ReasonSMTPRefused, Code: code}
	case errors.Is(err, mail.ErrTLS):
		return Outcome{Reason: ReasonSMTPTLS, Code: code}
	case errors.Is(err, mail.ErrPlainNotPrivate):
		return Outcome{Reason: ReasonSMTPPlain}
	}
	return Outcome{Reason: reasonFor(err)}
}
