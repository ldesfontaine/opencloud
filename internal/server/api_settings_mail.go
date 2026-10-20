package server

import (
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
	relay, err := s.settings.MailRelay(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, mailSettingsToJSON(relay))
}

func (s *Server) setMailSettings(w http.ResponseWriter, r *http.Request) {
	var request mailSettingsRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	relay, err := s.settings.SetMailRelay(r.Context(), settings.MailRelayChange{
		Relay: mail.Relay{
			Host: request.Host, Port: request.Port, TLS: mail.TLSMode(request.TLS),
			Username: request.Username, Password: request.Password, From: request.From,
		},
		ClearPassword: request.ClearPassword,
	})
	if s.failed(w, r, err, nil) {
		return
	}
	s.logger.Info("mail relay changed", "configured", relay.Configured(), "host", relay.Host, "tls", string(relay.TLS))
	s.writeAPI(w, http.StatusOK, mailSettingsToJSON(relay))
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
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, channelTestResponse{OK: outcome.OK(), Reason: string(outcome.Reason), Code: outcome.Code})
}
