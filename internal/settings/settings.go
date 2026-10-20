package settings

import (
	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/mail"
)

// Settings sont les réglages que l'opérateur change dans l'interface ; ils
// vivent en base (internal/store). Les étiquettes toml ne servent qu'à
// reprendre l'ancien settings.toml : voir ImportLegacyFile.
type Settings struct {
	// Langue de l'interface ; vide tant que l'opérateur n'a rien choisi.
	Language lang.Code `toml:"language"`
	// La page de statut publique : son titre, son annonce en texte brut,
	// sa langue. Vides : pas de titre, pas d'annonce, la langue de
	// l'interface.
	StatusTitle        string    `toml:"status_title"`
	StatusAnnouncement string    `toml:"status_announcement"`
	StatusLanguage     lang.Code `toml:"status_language"`
	// Les seuils d'alerte du disque, en pourcentage ; zéro tant que
	// l'opérateur n'a rien réglé, et le moteur prend alors les siens.
	DiskAttentionPercent int `toml:"disk_attention_percent"`
	DiskDangerPercent    int `toml:"disk_danger_percent"`
	// Le relais SMTP du canal e-mail. L'API ne renvoie jamais le mot de
	// passe.
	MailHost     string `toml:"mail_host,omitempty"`
	MailPort     int    `toml:"mail_port,omitempty"`
	MailTLS      string `toml:"mail_tls,omitempty"`
	MailUsername string `toml:"mail_username,omitempty"`
	MailPassword string `toml:"mail_password,omitempty"`
	MailFrom     string `toml:"mail_from,omitempty"`
	// La fin de support est activée tant que l'opérateur ne l'a pas coupée :
	// la machine openCloud lit endoflife.date une fois par jour, deux au
	// plus. Le réglage dit « coupée » pour que son absence vaille « activée ».
	LifecycleDisabled bool `toml:"lifecycle_disabled,omitempty"`
}

// InterfaceLanguage rend la langue de l'interface : celle du réglage, sinon
// la langue par défaut.
func (s Settings) InterfaceLanguage() lang.Code {
	if s.Language == "" {
		return lang.Default
	}
	return s.Language
}

// DiskThresholds rend les seuils en vigueur : ceux du réglage, sinon ceux
// du produit.
func (s Settings) DiskThresholds() alert.DiskThresholds {
	return alert.DiskThresholdsOr(s.DiskAttentionPercent, s.DiskDangerPercent)
}

// LifecycleEnabled : la fin de support tourne, sauf si l'opérateur l'a
// coupée.
func (s Settings) LifecycleEnabled() bool {
	return !s.LifecycleDisabled
}

// MailRelay rend le relais réglé ; un port absent suit le mode, un mode
// absent est STARTTLS.
func (s Settings) MailRelay() mail.Relay {
	mode := mail.TLSMode(s.MailTLS)
	if mode == "" {
		mode = mail.TLSStartTLS
	}
	port := s.MailPort
	if port == 0 {
		port = mail.DefaultPortFor(mode)
	}
	return mail.Relay{
		Host: s.MailHost, Port: port, TLS: mode, Username: s.MailUsername, Password: s.MailPassword, From: s.MailFrom,
	}
}

// SetMailRelay écrit le relais ; un relais sans hôte efface tout.
func (s *Settings) SetMailRelay(relay mail.Relay) {
	if relay.Host == "" {
		relay = mail.Relay{}
	}
	s.MailHost, s.MailPort, s.MailTLS = relay.Host, relay.Port, string(relay.TLS)
	s.MailUsername, s.MailPassword, s.MailFrom = relay.Username, relay.Password, relay.From
}
