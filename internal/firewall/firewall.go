package firewall

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ldesfontaine/opencloud/internal/refusal"
)

const (
	// KindOPNsense est le seul connecteur livré ; un autre pare-feu ajoute son
	// genre ici et son connecteur dans un sous-paquet.
	KindOPNsense = "opnsense"

	maxNameRunes     = 80
	maxURLLength     = 255
	minCredentialLen = 8
	maxCredentialLen = 200
	pinHexLength     = 64
	// Au-delà, ce n'est plus une installation qu'on regarde à l'écran.
	MaxFirewalls = 20
)

var (
	// ErrNotFound : aucun pare-feu ne porte cet identifiant.
	ErrNotFound = errors.New("firewall not declared")

	ErrNameInvalid   = refusal.Invalid("firewall.name_invalid", "the name must be 1 to 80 characters, without control characters")
	ErrNameTaken     = refusal.Conflict("firewall.name_taken", "another firewall already has this name")
	ErrKindInvalid   = refusal.Invalid("firewall.kind_invalid", "the connector is not one this server knows")
	ErrURLInvalid    = refusal.Invalid("firewall.url_invalid", "the API address must be https://host[:port], without path")
	ErrKeyInvalid    = refusal.Invalid("firewall.key_invalid", "the API key must be 8 to 200 printable characters, without space")
	ErrSecretInvalid = refusal.Invalid("firewall.secret_invalid", "the API secret must be 8 to 200 printable characters, without space")
	ErrPinInvalid    = refusal.Invalid("firewall.pin_invalid", "the pin must be a SHA-256 fingerprint in hexadecimal")
	ErrTooMany       = refusal.Conflict("firewall.too_many", "the number of firewalls is at its limit")

	// Les quatre façons dont l'API ne répond pas : elles se lisent sur la
	// déclaration refusée comme sur le relevé qui échoue (last_error).
	ErrUnreachable  = refusal.Invalid("firewall.unreachable", "the firewall API did not answer")
	ErrCertificate  = refusal.Invalid("firewall.certificate", "the certificate of the firewall API is not the one expected")
	ErrUnauthorized = refusal.Invalid("firewall.unauthorized", "the firewall API refused the key")
	ErrBadResponse  = refusal.Invalid("firewall.bad_response", "the firewall API answered something unexpected")
)

// Definition est ce que l'opérateur saisit pour déclarer un pare-feu.
type Definition struct {
	Name      string
	Kind      string
	APIURL    string
	APIKey    string
	APISecret string
	// Vide : le magasin du système juge le certificat.
	Pin string
}

// Firewall est le pare-feu déclaré, sans son secret.
type Firewall struct {
	ID      string
	Name    string
	Kind    string
	APIURL  string
	APIKey  string
	Pin     string
	Version string
	// VerifiedAt : quand la paire de clés a répondu à la déclaration.
	VerifiedAt time.Time
	// LastReadAt est le dernier relevé réussi ; zéro tant qu'aucun ne l'a été.
	LastReadAt time.Time
	// LastError est la clé du refus du dernier relevé qui a échoué, vide s'il
	// a réussi.
	LastError string
	CreatedAt time.Time
}

// Credentials est le pare-feu avec son secret : il ne sort de la base que
// pour parler à l'API.
type Credentials struct {
	Firewall
	APISecret string
}

type Interface struct {
	// L'identifiant chez le pare-feu (lan, opt1).
	Name   string
	Device string
	// Le rôle que le pare-feu lui donne : sa description (WAN, LAN…).
	Role    string
	Address string
	// up ou down, tel que le pare-feu le dit.
	Status string
}

type Rule struct {
	UUID            string
	Interface       string
	Action          string
	Direction       string
	Protocol        string
	Source          string
	SourcePort      string
	Destination     string
	DestinationPort string
	Description     string
	Enabled         bool
}

type Lease struct {
	Address   string
	MAC       string
	Hostname  string
	Interface string
	// Le serveur DHCP qui l'a donné : dnsmasq, kea ou isc.
	Server   string
	IsStatic bool
	// Zéro : le pare-feu ne dit pas quand le bail expire.
	ExpiresAt time.Time
}

// Les états d'une passerelle, dans les mots d'openCloud : chaque connecteur
// traduit ceux de son constructeur.
const (
	GatewayOnline   = "online"
	GatewayOffline  = "offline"
	GatewayDegraded = "degraded"
	GatewayUnknown  = "unknown"
)

type Gateway struct {
	Name    string
	Address string
	// L'un des états ci-dessus.
	Status string
	// IsMonitored est faux quand le pare-feu ne sonde pas la passerelle : le
	// délai et la perte ne disent alors rien.
	IsMonitored bool
	DelayMillis float64
	LossPercent float64
}

// Snapshot est un relevé complet : il remplace le précédent ou rien.
type Snapshot struct {
	Version    string
	Interfaces []Interface
	Rules      []Rule
	Leases     []Lease
	Gateways   []Gateway
}

// View est ce que la fiche d'un pare-feu montre.
type View struct {
	Firewall
	Interfaces []Interface
	Rules      []Rule
	Leases     []Lease
	Gateways   []Gateway
}

// Summary est ce que la liste dit de chaque pare-feu, sans le relevé entier.
type Summary struct {
	Firewall
	Interfaces int
	Rules      int
	Leases     int
	Gateways   int
}

var (
	// De l'ASCII imprimable sans espace : la paire entre dans un en-tête.
	credentialPattern = regexp.MustCompile(`^[\x21-\x7e]+$`)
	pinPattern        = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Normalize met la saisie en forme canonique et rend le premier refus. Le
// secret se ressaisit à chaque déclaration : il n'est jamais relu pour
// l'afficher.
func (d Definition) Normalize() (Definition, error) {
	d.Name = strings.TrimSpace(d.Name)
	if !isValidName(d.Name) {
		return Definition{}, ErrNameInvalid
	}
	if d.Kind != KindOPNsense {
		return Definition{}, ErrKindInvalid
	}
	address, err := normalizeURL(d.APIURL)
	if err != nil {
		return Definition{}, err
	}
	d.APIURL = address
	d.APIKey = strings.TrimSpace(d.APIKey)
	if !isValidCredential(d.APIKey) {
		return Definition{}, ErrKeyInvalid
	}
	if !isValidCredential(d.APISecret) {
		return Definition{}, ErrSecretInvalid
	}
	d.Pin, err = normalizePin(d.Pin)
	if err != nil {
		return Definition{}, err
	}
	return d, nil
}

func isValidName(name string) bool {
	if name == "" || utf8.RuneCountInString(name) > maxNameRunes {
		return false
	}
	for _, character := range name {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func isValidCredential(value string) bool {
	return len(value) >= minCredentialLen && len(value) <= maxCredentialLen && credentialPattern.MatchString(value)
}

// normalizeURL garde le schéma, l'hôte et le port : l'API se joint en https,
// et ni identifiants, ni chemin, ni requête n'ont de sens ici.
func normalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxURLLength {
		return "", ErrURLInvalid
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return "", ErrURLInvalid
	}
	if (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return "", ErrURLInvalid
	}
	return "https://" + parsed.Host, nil
}

// normalizePin accepte ce que openssl écrit (AA:BB:…), la ligne entière
// comprise (« sha256 Fingerprint=AA:BB:… »), comme l'hexadécimal nu, avec ou
// sans « sha256: » devant.
func normalizePin(raw string) (string, error) {
	pin := strings.ToLower(strings.TrimSpace(raw))
	if label, value, found := strings.Cut(pin, "fingerprint="); found && strings.TrimSpace(label) == "sha256" {
		pin = strings.TrimSpace(value)
	}
	pin = strings.TrimPrefix(pin, "sha256:")
	pin = strings.ReplaceAll(pin, ":", "")
	if pin == "" {
		return "", nil
	}
	if len(pin) != pinHexLength || !pinPattern.MatchString(pin) {
		return "", ErrPinInvalid
	}
	return pin, nil
}
