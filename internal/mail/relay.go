package mail

import (
	"errors"
	"net"
	"net/mail"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// TLSMode dit comment la connexion au relais est chiffrée : STARTTLS sur
// le port de soumission, TLS dès la connexion sur 465, ou rien, réservé à
// un relais sur la machine ou le réseau privé.
type TLSMode string

const (
	TLSStartTLS TLSMode = "starttls"
	TLSImplicit TLSMode = "tls"
	TLSNone     TLSMode = "none"
)

func TLSModes() []TLSMode {
	return []TLSMode{TLSStartTLS, TLSImplicit, TLSNone}
}

const (
	DefaultPort     = 587
	MaxHost         = 253
	MaxUsername     = 256
	MaxPassword     = 256
	MaxAddress      = 254
	MaxRecipients   = 10
	implicitTLSPort = 465
)

// Relay est le relais de l'opérateur. Le mot de passe ne sort jamais du
// processus : HasPassword dit seulement qu'il y en a un.
type Relay struct {
	Host     string
	Port     int
	TLS      TLSMode
	Username string
	Password string
	From     string
}

var (
	ErrHostInvalid     = refusal.Invalid("settings.mail_host_invalid", "mail: relay host invalid")
	ErrPortInvalid     = refusal.Invalid("settings.mail_port_invalid", "mail: relay port invalid")
	ErrTLSModeInvalid  = refusal.Invalid("settings.mail_tls_invalid", "mail: tls mode invalid")
	ErrPlainNotPrivate = refusal.Invalid("settings.mail_plain_public", "mail: plain relay outside loopback or private network")
	ErrUsernameInvalid = refusal.Invalid("settings.mail_username_invalid", "mail: username invalid")
	ErrPasswordInvalid = refusal.Invalid("settings.mail_password_invalid", "mail: password invalid")
	ErrFromInvalid     = refusal.Invalid("settings.mail_from_invalid", "mail: sender address invalid")
	ErrAddressInvalid  = errors.New("mail: recipient address invalid")
	ErrNoRecipient     = errors.New("mail: no recipient")
	ErrTooManyAddress  = errors.New("mail: too many recipients")
)

// Configured dit si un relais est réglé : sans hôte, rien ne part.
func (r Relay) Configured() bool {
	return r.Host != ""
}

// Validate vérifie ce qu'on peut vérifier sans réseau. Un relais en clair
// nommé par son nom se revérifie à la connexion, sur l'adresse obtenue.
func (r Relay) Validate() error {
	if !isHost(r.Host) {
		return ErrHostInvalid
	}
	if r.Port < 1 || r.Port > 65535 {
		return ErrPortInvalid
	}
	if !slices.Contains(TLSModes(), r.TLS) {
		return ErrTLSModeInvalid
	}
	if ip := net.ParseIP(r.Host); r.TLS == TLSNone && ip != nil && !IsPrivate(ip) {
		return ErrPlainNotPrivate
	}
	if len(r.Username) > MaxUsername || !utf8.ValidString(r.Username) || strings.ContainsAny(r.Username, "\r\n\x00") {
		return ErrUsernameInvalid
	}
	if len(r.Password) > MaxPassword || !utf8.ValidString(r.Password) || strings.ContainsAny(r.Password, "\r\n\x00") {
		return ErrPasswordInvalid
	}
	if !IsAddress(r.From) {
		return ErrFromInvalid
	}
	return nil
}

// isHost admet un nom ou une adresse IP littérale, sans port ni chemin.
func isHost(host string) bool {
	if net.ParseIP(host) != nil {
		return true
	}
	return host != "" && len(host) <= MaxHost && !strings.ContainsAny(host, " \t\r\n/:@[]")
}

// IsPrivate dit où un relais en clair est admis : la boucle locale ou un
// réseau privé, là où le mot de passe ne traverse pas Internet.
func IsPrivate(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate()
}

// IsAddress n'admet qu'une adresse nue, « nom@domaine » : pas de nom
// affiché, pas de liste, rien qui puisse ouvrir un en-tête de plus.
func IsAddress(raw string) bool {
	if raw == "" || len(raw) > MaxAddress || strings.ContainsAny(raw, " \t\r\n<>,;\"\\") {
		return false
	}
	parsed, err := mail.ParseAddress(raw)
	if err != nil || parsed.Address != raw || parsed.Name != "" {
		return false
	}
	at := strings.LastIndexByte(raw, '@')
	return at > 0 && strings.Contains(raw[at+1:], ".")
}

// ValidateRecipients borne et vérifie une liste de destinataires.
func ValidateRecipients(recipients []string) error {
	if len(recipients) == 0 {
		return ErrNoRecipient
	}
	if len(recipients) > MaxRecipients {
		return ErrTooManyAddress
	}
	for _, recipient := range recipients {
		if !IsAddress(recipient) {
			return ErrAddressInvalid
		}
	}
	return nil
}

// DefaultPortFor est le port qui va avec le mode : 465 pour TLS dès la
// connexion, 587 sinon.
func DefaultPortFor(mode TLSMode) int {
	if mode == TLSImplicit {
		return implicitTLSPort
	}
	return DefaultPort
}
