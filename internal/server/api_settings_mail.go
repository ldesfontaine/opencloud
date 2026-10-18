package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/ldesfontaine/opencloud/internal/mail"
	"github.com/ldesfontaine/opencloud/internal/settings"
)

// Le relais SMTP du canal e-mail, réglé sous Paramètres. Le mot de passe
// ne revient jamais : has_password dit seulement qu'il y en a un.
type mailSettingsJSON struct {
	Configured  bool   `json:"configured"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	TLS         string `json:"tls"`
	Username    string `json:"username"`
	HasPassword bool   `json:"has_password"`
	From        string `json:"from"`
}

// Un hôte vide retire le relais ; un mot de passe vide garde celui en
// place, clear_password le retire.
type mailSettingsRequest struct {
	Host          string `json:"host"`
	Port          int    `json:"port"`
	TLS           string `json:"tls"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	ClearPassword bool   `json:"clear_password"`
	From          string `json:"from"`
}

type mailTestRequest struct {
	To string `json:"to"`
}

func mailSettingsToJSON(relay mail.Relay) mailSettingsJSON {
	return mailSettingsJSON{
		Configured: relay.Configured(), Host: relay.Host, Port: relay.Port, TLS: string(relay.TLS),
		Username: relay.Username, HasPassword: relay.Password != "", From: relay.From,
	}
}

func (s *Server) getMailSettings(w http.ResponseWriter, r *http.Request) {
	current, err := s.settings.LoadSettings(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusOK, mailSettingsToJSON(current.MailRelay()))
}

func (s *Server) setMailSettings(w http.ResponseWriter, r *http.Request) {
	var request mailSettingsRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	relay, err := s.saveMailRelay(r.Context(), request)
	if key, refused := mailRefusal(err); refused {
		s.apiRefuse(w, http.StatusUnprocessableEntity, key)
		return
	}
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.logger.Info("mail relay changed", "configured", relay.Configured(), "host", relay.Host, "tls", string(relay.TLS))
	s.writeAPI(w, http.StatusOK, mailSettingsToJSON(relay))
}

// saveMailRelay valide puis n'écrit que les clés du relais : la langue et
// les seuils vivent sur la même ligne, et UpdateSettings les préserve.
func (s *Server) saveMailRelay(ctx context.Context, request mailSettingsRequest) (mail.Relay, error) {
	relay := mail.Relay{
		Host: strings.TrimSpace(request.Host), Port: request.Port, TLS: mail.TLSMode(request.TLS),
		Username: request.Username, Password: request.Password, From: strings.TrimSpace(request.From),
	}
	// Un mot de passe laissé vide garde celui de la base, relu dans la même
	// transaction ; il a été validé quand il a été écrit.
	keepPassword := relay.Password == "" && !request.ClearPassword
	if relay.Port == 0 {
		relay.Port = mail.DefaultPortFor(relay.TLS)
	}
	if relay.Host != "" {
		if err := relay.Validate(); err != nil {
			return mail.Relay{}, err
		}
	}
	current, err := s.settings.UpdateSettings(ctx, func(current *settings.Settings) {
		if keepPassword {
			relay.Password = current.MailPassword
		}
		current.SetMailRelay(relay)
	})
	if err != nil {
		return mail.Relay{}, err
	}
	return current.MailRelay(), nil
}

// testMailSettings envoie l'e-mail de test vers une adresse saisie, par
// le relais enregistré, et répond comme le test d'un canal.
func (s *Server) testMailSettings(w http.ResponseWriter, r *http.Request) {
	var request mailTestRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	recipient := strings.TrimSpace(request.To)
	if !mail.IsAddress(recipient) {
		s.apiRefuse(w, http.StatusUnprocessableEntity, "settings.mail_to_invalid")
		return
	}
	outcome, err := s.notifier.TestMail(r.Context(), recipient)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusOK, channelTestResponse{OK: outcome.OK(), Reason: string(outcome.Reason), Code: outcome.Code})
}

func mailRefusal(err error) (string, bool) {
	switch {
	case errors.Is(err, mail.ErrHostInvalid):
		return "settings.mail_host_invalid", true
	case errors.Is(err, mail.ErrPortInvalid):
		return "settings.mail_port_invalid", true
	case errors.Is(err, mail.ErrTLSModeInvalid):
		return "settings.mail_tls_invalid", true
	case errors.Is(err, mail.ErrPlainNotPrivate):
		return "settings.mail_plain_public", true
	case errors.Is(err, mail.ErrUsernameInvalid):
		return "settings.mail_username_invalid", true
	case errors.Is(err, mail.ErrPasswordInvalid):
		return "settings.mail_password_invalid", true
	case errors.Is(err, mail.ErrFromInvalid):
		return "settings.mail_from_invalid", true
	}
	return "", false
}
