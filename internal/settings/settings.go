package settings

import (
	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/mail"
)

// Settings sont les réglages que l'opérateur change dans l'interface ; ils
// vivent en base (internal/store).
type Settings struct {
	// Langue de l'interface ; vide tant que l'opérateur n'a rien choisi.
	Language lang.Code
	// La page de statut publique : son titre, son annonce en texte brut,
	// sa langue. Vides : pas de titre, pas d'annonce, la langue de
	// l'interface.
	StatusTitle        string
	StatusAnnouncement string
	StatusLanguage     lang.Code
	// Les seuils d'alerte du disque, en pourcentage ; zéro tant que
	// l'opérateur n'a rien réglé, et le moteur prend alors les siens.
	DiskAttentionPercent int
	DiskDangerPercent    int
	// Le relais SMTP du canal e-mail. L'API ne renvoie jamais le mot de
	// passe.
	MailHost     string
	MailPort     int
	MailTLS      string
	MailUsername string
	MailPassword string
	MailFrom     string
	// La fin de support est activée tant que l'opérateur ne l'a pas coupée :
	// la machine openCloud lit endoflife.date une fois par jour, deux au
	// plus. Le réglage dit « coupée » pour que son absence vaille « activée ».
	LifecycleDisabled bool
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
