package alert

import (
	"errors"
	"net"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ldesfontaine/opencloud/internal/egress"
)

// Format est la forme du corps envoyé : celle d'openCloud, structurée ;
// du texte brut, ce que lit ntfy ; un embed Discord ; un message Slack,
// que Mattermost et Rocket.Chat lisent aussi. Un seul genre de canal, le
// webhook : tout ce qui a une URL entrante se branche.
type Format string

const (
	FormatJSON    Format = "json"
	FormatText    Format = "text"
	FormatDiscord Format = "discord"
	FormatSlack   Format = "slack"
)

func isFormat(format Format) bool {
	switch format {
	case FormatJSON, FormatText, FormatDiscord, FormatSlack:
		return true
	}
	return false
}

const (
	MaxChannelName   = 80
	MaxChannelURL    = 2048
	MaxChannelSecret = 256
	MaxChannels      = 32
)

// Channel est un canal tel que la base le connaît. Le secret sert à signer
// le corps ; il ne sort jamais du processus : HasSecret dit seulement qu'il
// y en a un.
type Channel struct {
	ID     int64
	Name   string
	URL    string
	Format Format
	Secret string
	// HasSecret est dérivé à la lecture, jamais stocké.
	HasSecret bool
	// MinSeverity : le canal ne reçoit que ce qui atteint cette gravité.
	MinSeverity Severity
	// NotifyResolve : prévenir aussi quand l'alerte se résout.
	NotifyResolve bool
	Enabled       bool
	CreatedAt     time.Time
}

// Wants dit si le canal doit recevoir cet événement de cette alerte.
func (c Channel) Wants(alert Alert, event Event) bool {
	if !c.Enabled || rank(alert.Severity) < rank(c.MinSeverity) {
		return false
	}
	return event != EventResolved || c.NotifyResolve
}

// ChannelDefinition est ce que l'opérateur donne pour créer ou modifier un
// canal. Secret vide à la modification garde le secret en place ; le
// retirer est explicite.
type ChannelDefinition struct {
	Name          string
	URL           string
	Format        Format
	Secret        string
	ClearSecret   bool
	MinSeverity   Severity
	NotifyResolve bool
	Enabled       bool
}

var (
	ErrChannelNameInvalid     = errors.New("alert: channel name invalid")
	ErrChannelURLInvalid      = errors.New("alert: channel url invalid")
	ErrChannelURLForbidden    = errors.New("alert: channel url is a link-local address")
	ErrChannelFormatInvalid   = errors.New("alert: channel format invalid")
	ErrChannelSecretInvalid   = errors.New("alert: channel secret invalid")
	ErrChannelSeverityInvalid = errors.New("alert: channel severity invalid")
	ErrTooManyChannels        = errors.New("alert: too many channels")
	ErrSilenceInvalid         = errors.New("alert: silence needs a kind or an object")
	ErrSilenceDurationInvalid = errors.New("alert: silence duration invalid")
)

// Validate vérifie ce qu'on peut vérifier sans réseau : ce que le nom
// résout se revérifie au moment d'envoyer, par la garde de sortie.
func (d ChannelDefinition) Validate() error {
	name := strings.TrimSpace(d.Name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > MaxChannelName {
		return ErrChannelNameInvalid
	}
	if err := validateURL(d.URL); err != nil {
		return err
	}
	if !isFormat(d.Format) {
		return ErrChannelFormatInvalid
	}
	if len(d.Secret) > MaxChannelSecret || !utf8.ValidString(d.Secret) {
		return ErrChannelSecretInvalid
	}
	if !isSeverity(d.MinSeverity) {
		return ErrChannelSeverityInvalid
	}
	return nil
}

func validateURL(raw string) error {
	if raw == "" || len(raw) > MaxChannelURL || strings.ContainsAny(raw, " \t\r\n") {
		return ErrChannelURLInvalid
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return ErrChannelURLInvalid
	}
	if ip := net.ParseIP(parsed.Hostname()); ip != nil && egress.Forbidden(ip) {
		return ErrChannelURLForbidden
	}
	return nil
}

// IsPlain dit si le canal parle en clair : l'interface l'écrit, l'envoi
// n'en est pas empêché, un ntfy sur le LAN est un usage voulu.
func (c Channel) IsPlain() bool {
	return strings.HasPrefix(c.URL, "http://")
}
