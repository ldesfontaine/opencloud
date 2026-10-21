package settings

import (
	"strconv"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/mail"
)

// Les groupes de réglages que le journal nomme : l'objet d'un réglage est
// son groupe. Les clés de la page de statut sont journalisées par la page.
const (
	keyLanguage  = "language"
	keyMailRelay = "mail_relay"
	keyDisk      = "disk"
	keyLifecycle = "lifecycle"
)

// SetAuditor branche le journal d'audit ; nil est toléré.
func (s *Service) SetAuditor(auditor *audit.Recorder) {
	s.auditor = auditor
}

func settingsEvent(key string, details audit.Details) audit.Event {
	return audit.Event{Action: audit.SettingsUpdate, ObjectID: key, ObjectName: key, Details: details}
}

// mailRelayEvent : le relais sans son mot de passe ; seulement s'il a changé.
func mailRelayEvent(written mail.Relay, passwordChanged bool) audit.Event {
	return settingsEvent(keyMailRelay, audit.Details{
		"host":             written.Host,
		"port":             strconv.Itoa(written.Port),
		"tls":              string(written.TLS),
		"username":         written.Username,
		"from":             written.From,
		"password_changed": strconv.FormatBool(passwordChanged),
	})
}
