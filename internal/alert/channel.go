package alert

import (
	"net"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ldesfontaine/opencloud/internal/egress"
	"github.com/ldesfontaine/opencloud/internal/mail"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

// Format est la forme du corps envoyé : celle d'openCloud, structurée ;
// du texte brut, ce que lit ntfy ; un embed Discord ; un message Slack,
// que Mattermost et Rocket.Chat lisent aussi. Un seul genre de canal, le
// webhook : tout ce qui a une URL entrante se branche. L'e-mail n'a pas
// d'URL : il part par le relais SMTP réglé sous Paramètres, vers les
// adresses de sa destination. Matrix n'a pas de webhook entrant : un bot y
// écrit par l'API client-serveur, avec son jeton d'accès, dans un salon où
// il a été invité.
type Format string

const (
	FormatJSON    Format = "json"
	FormatText    Format = "text"
	FormatDiscord Format = "discord"
	FormatSlack   Format = "slack"
	FormatEmail   Format = "email"
	FormatMatrix  Format = "matrix"
)

func Formats() []Format {
	return []Format{FormatJSON, FormatText, FormatDiscord, FormatSlack, FormatEmail, FormatMatrix}
}

func isFormat(format Format) bool {
	return slices.Contains(Formats(), format)
}

const (
	MaxChannelName   = 80
	MaxChannelURL    = 2048
	MaxChannelSecret = 256
	MaxChannels      = 32
	// Dix adresses de 254 caractères et leurs séparateurs.
	MaxChannelDestination = 2600
	// La taille maximale d'un identifiant Matrix, tous types confondus.
	MaxMatrixRoomID = 255
)

// Channel est un canal tel que la base le connaît. Le secret sert à signer
// le corps ; il ne sort jamais du processus : HasSecret dit seulement qu'il
// y en a un.
type Channel struct {
	ID     int64
	Name   string
	URL    string
	Format Format
	// Destination dit où le message arrive quand l'URL ne le dit pas.
	Destination string
	Secret      string
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
	Destination   string
	Secret        string
	ClearSecret   bool
	MinSeverity   Severity
	NotifyResolve bool
	Enabled       bool
}

var (
	ErrChannelNameInvalid     = refusal.Invalid("channel.name_invalid", "alert: channel name invalid")
	ErrChannelURLInvalid      = refusal.Invalid("channel.url_invalid", "alert: channel url invalid")
	ErrChannelURLForbidden    = refusal.Invalid("channel.url_forbidden", "alert: channel url is a link-local address")
	ErrChannelFormatInvalid   = refusal.Invalid("channel.format_invalid", "alert: channel format invalid")
	ErrChannelSecretInvalid   = refusal.Invalid("channel.secret_invalid", "alert: channel secret invalid")
	ErrChannelSeverityInvalid = refusal.Invalid("channel.severity_invalid", "alert: channel severity invalid")
	ErrChannelRecipients      = refusal.Invalid("channel.recipients_invalid", "alert: channel recipients invalid")
	ErrChannelHomeserver      = refusal.Invalid("channel.matrix_homeserver_invalid", "alert: matrix homeserver must be an https url")
	ErrChannelRoomInvalid     = refusal.Invalid("channel.matrix_room_invalid", "alert: matrix room id invalid")
	ErrChannelTokenMissing    = refusal.Invalid("channel.matrix_token_missing", "alert: matrix access token missing")
	ErrTooManyChannels        = refusal.Invalid("channel.too_many", "alert: too many channels")
)

// Validate vérifie ce qu'on peut vérifier sans réseau : ce que le nom
// résout se revérifie au moment d'envoyer, par la garde de sortie.
func (d ChannelDefinition) Validate() error {
	name := strings.TrimSpace(d.Name)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > MaxChannelName {
		return ErrChannelNameInvalid
	}
	if !isFormat(d.Format) {
		return ErrChannelFormatInvalid
	}
	if err := d.validateTarget(); err != nil {
		return err
	}
	if len(d.Secret) > MaxChannelSecret || !utf8.ValidString(d.Secret) {
		return ErrChannelSecretInvalid
	}
	if !isSeverity(d.MinSeverity) {
		return ErrChannelSeverityInvalid
	}
	return nil
}

// validateTarget vérifie où le canal envoie : l'URL d'un webhook, les
// adresses d'un e-mail, le serveur d'accueil et le salon d'un canal Matrix.
func (d ChannelDefinition) validateTarget() error {
	switch d.Format {
	case FormatEmail:
		if len(d.Destination) > MaxChannelDestination || mail.ValidateRecipients(Recipients(d.Destination)) != nil {
			return ErrChannelRecipients
		}
		return nil
	case FormatMatrix:
		if err := validateURL(d.URL); err != nil {
			return err
		}
		return d.validateMatrix()
	}
	return validateURL(d.URL)
}

// validateMatrix : le jeton voyage dans un en-tête, le serveur d'accueil
// doit donc être en https, et le salon nommé par son identifiant.
func (d ChannelDefinition) validateMatrix() error {
	parsed, err := url.Parse(d.URL)
	if err != nil || parsed.Scheme != "https" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ErrChannelHomeserver
	}
	if !IsMatrixRoomID(d.Destination) {
		return ErrChannelRoomInvalid
	}
	return nil
}

// normalized range la définition selon son format : un e-mail n'a ni URL
// ni secret, ses adresses sont écrites d'une seule façon ; un salon Matrix
// est nommé sans espace autour ; un webhook n'a pas de destination.
func (d ChannelDefinition) normalized() ChannelDefinition {
	switch d.Format {
	case FormatEmail:
		d.URL = ""
		d.Secret = ""
		d.ClearSecret = true
		d.Destination = strings.Join(Recipients(d.Destination), ", ")
	case FormatMatrix:
		d.Destination = strings.TrimSpace(d.Destination)
	default:
		d.Destination = ""
	}
	return d
}

// Recipients lit les adresses d'un canal e-mail, séparées par des
// virgules, des points-virgules ou des espaces ; un doublon ne compte
// qu'une fois.
func Recipients(destination string) []string {
	fields := strings.FieldsFunc(destination, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	recipients := []string{}
	for _, field := range fields {
		if !slices.Contains(recipients, field) {
			recipients = append(recipients, field)
		}
	}
	return recipients
}

// IsMatrixRoomID admet « !opaque:serveur », ce que montrent les réglages
// avancés d'un salon. Un alias « #salon:serveur » ne l'est pas : il se
// résout, et peut changer de salon.
func IsMatrixRoomID(raw string) bool {
	if len(raw) < 4 || len(raw) > MaxMatrixRoomID || raw[0] != '!' || strings.ContainsAny(raw, " \t\r\n/?#") {
		return false
	}
	opaque, server, found := strings.Cut(raw[1:], ":")
	return found && opaque != "" && server != ""
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
