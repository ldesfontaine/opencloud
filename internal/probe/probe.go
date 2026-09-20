package probe

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// Les états d'une sonde, dans le vocabulaire de la direction artistique :
// Nouveau, En ligne, Dégradé, Hors ligne, En pause.
type Status string

const (
	StatusNew      Status = "new"
	StatusUp       Status = "up"
	StatusDegraded Status = "degraded"
	StatusDown     Status = "down"
	StatusPaused   Status = "paused"
)

// Kind est ce que la sonde parle.
type Kind string

const (
	KindHTTP Kind = "http"
	KindTCP  Kind = "tcp"
)

// Outcome est ce qu'un essai a donné. Dégradé est un succès : l'hôte
// répond et sert, seule sa chaîne de certificats est refusée. L'uptime
// n'en est donc pas entamé, seul l'état change.
type Outcome string

const (
	OutcomeUp       Outcome = "up"
	OutcomeDegraded Outcome = "degraded"
	OutcomeDown     Outcome = "down"
)

func (o Outcome) IsSuccess() bool {
	return o == OutcomeUp || o == OutcomeDegraded
}

// Reason dit pourquoi un essai a donné ce qu'il a donné, par un mot d'une
// liste fermée : le front le traduit, il ne lit jamais un texte du serveur.
type Reason string

const (
	ReasonNone         Reason = ""
	ReasonTimeout      Reason = "timeout"
	ReasonRefused      Reason = "refused"
	ReasonDNS          Reason = "dns"
	ReasonUnreachable  Reason = "unreachable"
	ReasonAddress      Reason = "address"
	ReasonStatus       Reason = "status"
	ReasonBody         Reason = "body"
	ReasonRedirect     Reason = "redirect"
	ReasonTLSUntrusted Reason = "tls_untrusted"
	ReasonTLSExpired   Reason = "tls_expired"
	ReasonTLSHostname  Reason = "tls_hostname"
)

func isReason(reason Reason) bool {
	switch reason {
	case ReasonNone, ReasonTimeout, ReasonRefused, ReasonDNS, ReasonUnreachable, ReasonAddress,
		ReasonStatus, ReasonBody, ReasonRedirect, ReasonTLSUntrusted, ReasonTLSExpired, ReasonTLSHostname:
		return true
	}
	return false
}

// OCSP est ce que l'agrafe remise pendant la poignée de main a dit de la
// révocation, par un mot d'une liste fermée. Vide : la cible n'agrafe
// rien, et openCloud ne contacte aucun répondeur pour le savoir.
type OCSP string

const (
	OCSPNone    OCSP = ""
	OCSPGood    OCSP = "good"
	OCSPRevoked OCSP = "revoked"
	OCSPUnknown OCSP = "unknown"
)

func isOCSP(status OCSP) bool {
	switch status {
	case OCSPNone, OCSPGood, OCSPRevoked, OCSPUnknown:
		return true
	}
	return false
}

const (
	// Plus fin que l'intervalle du signal ne servirait à rien : les essais
	// remontent avec lui.
	MinInterval     = 30 * time.Second
	MaxInterval     = 24 * time.Hour
	DefaultInterval = time.Minute
	MinTimeout      = time.Second
	MaxTimeout      = 30 * time.Second
	DefaultTimeout  = 10 * time.Second

	// Les seuils amortissent la bascule : un hoquet réseau ne fait pas
	// clignoter la page, une cible qui revient doit le confirmer.
	DefaultFailureThreshold  = 3
	DefaultRecoveryThreshold = 2
	MaxThreshold             = 10

	MaxNameLength   = 80
	MaxTargetLength = 512
	MaxBodyLength   = 200
	MaxIssuerLength = 255

	// Combien de sondes une machine porte ; au-delà, la création est
	// refusée. Autant de goroutines chez l'agent, pas plus.
	MaxProbesPerMachine = 64
	// Ce qu'un signal peut porter d'essais : une machine coupée une
	// journée avec ses 64 sondes à 30 s en aurait bien plus, elle perdra
	// les plus anciens comme le reste.
	MaxResultsPerReport = 512
	// Ce que la sonde lit du corps pour y chercher le texte attendu.
	MaxReadBytes = 64 << 10
	// Les redirections suivies quand la sonde les suit.
	MaxRedirects = 5
	// Une durée d'essai au-delà est absurde : l'agent a une horloge folle.
	maxDurationMs = 10 * 60 * 1000
	// Un essai daté d'après cette avance est refusé : horloge fausse.
	maxFutureSkew = 5 * time.Minute

	// Ce qu'il reste à un certificat avant qu'openCloud le signale. Deux
	// seuils, pas cinq : sans destinataire, un seuil de plus ne fait
	// qu'un ton de plus. Le navigateur les dérive, le serveur ne les
	// applique pas.
	CertificateWarning = 30
	CertificateDanger  = 7

	// Le brut sert la fenêtre de 24 h, l'agrégat journalier les autres :
	// chaque fenêtre lit une table gardée strictement plus longtemps qu'elle.
	ResultRetention = 7 * 24 * time.Hour
	DayRetention    = 365 * 24 * time.Hour
)

// Probe est une sonde telle que la base la connaît. Les dates zéro disent
// « jamais ».
type Probe struct {
	ID   string
	Name string
	Kind Kind
	// L'URL d'une sonde HTTP, « hôte:port » d'une sonde TCP.
	Target string
	// La machine qui sonde ; jamais vide, la machine openCloud par défaut.
	MachineID   string
	MachineName string
	// Sans son flux, la machine ne sonde pas : l'état montré est le dernier
	// connu, et l'interface le dit.
	MachineOnline bool
	// Le service surveillé ; vide quand la sonde ne tient à aucun. Le
	// service supprimé, la sonde reste et perd son rattachement.
	ServiceID   string
	ServiceName string

	Status            Status
	Interval          time.Duration
	Timeout           time.Duration
	FailureThreshold  int
	RecoveryThreshold int
	// Propres à HTTP ; vides pour une sonde TCP.
	Method          string
	ExpectedStatus  string
	ExpectedBody    string
	FollowRedirects bool
	// TLS demande à une sonde TCP une poignée de main plutôt qu'une
	// simple connexion : c'est ce qui couvre un port chiffré qui ne parle
	// pas HTTP, SMTP ou IMAP. Sans objet pour une sonde HTTP, dont l'URL
	// dit déjà le protocole.
	TLS bool

	ConsecutiveFailures  int
	ConsecutiveSuccesses int
	LastCheckedAt        time.Time
	LastDurationMs       int64
	// nil tant qu'aucun essai n'a rapporté de code HTTP.
	LastCode   *int
	LastReason Reason
	// Le certificat vu au dernier essai chiffré. Un essai qui n'en voit
	// pas n'efface pas celui qu'on avait : une coupure ne fait pas
	// disparaître ce que la cible sert.
	Certificate *Certificate
	CreatedAt   time.Time
}

func (p Probe) IsPaused() bool {
	return p.Status == StatusPaused
}

// Task rend ce que l'agent a besoin de savoir pour sonder, et rien d'autre.
func (p Probe) Task() Task {
	return Task{
		ID:              p.ID,
		Kind:            p.Kind,
		Target:          p.Target,
		IntervalSeconds: int(p.Interval.Seconds()),
		TimeoutSeconds:  int(p.Timeout.Seconds()),
		Method:          p.Method,
		ExpectedStatus:  p.ExpectedStatus,
		ExpectedBody:    p.ExpectedBody,
		FollowRedirects: p.FollowRedirects,
		TLS:             p.TLS,
	}
}

// Certificate est la chaîne que la cible a présentée, réduite à des faits.
// L'échéance et la confiance en sont deux : un certificat d'autorité
// interne a une date parfaitement lisible, et c'est justement le cas où
// l'opérateur se fait avoir. Aucun de ces champs n'est déduit d'un autre
// ni supposé vrai.
type Certificate struct {
	Subject     string    `json:"subject"`
	Issuer      string    `json:"issuer"`
	NotBefore   time.Time `json:"not_before"`
	NotAfter    time.Time `json:"not_after"`
	Fingerprint string    `json:"fingerprint"`
	// ChainValid dit que la chaîne remonte à une autorité connue de la
	// machine qui a sondé, dates comprises.
	ChainValid bool `json:"chain_valid"`
	// HostnameMatch dit que le certificat couvre bien le nom demandé :
	// une chaîne impeccable peut servir le mauvais domaine.
	HostnameMatch bool `json:"hostname_match"`
	// OCSP est ce que l'agrafe a dit, quand la cible en a remis une.
	OCSP OCSP `json:"ocsp,omitempty"`
}

// Expired dit si la date de fin est passée. C'est un fait du certificat,
// pas un état de la sonde : un hôte qui sert un certificat expiré répond
// parfaitement.
func (c Certificate) Expired(now time.Time) bool {
	return c.NotAfter.Before(now)
}

// Result est un essai, tel qu'il part dans le signal puis s'écrit dans
// l'histoire.
type Result struct {
	ProbeID     string       `json:"probe_id"`
	CheckedAt   time.Time    `json:"checked_at"`
	Outcome     Outcome      `json:"outcome"`
	DurationMs  int64        `json:"duration_ms"`
	Code        *int         `json:"code,omitempty"`
	Reason      Reason       `json:"reason,omitempty"`
	Certificate *Certificate `json:"certificate,omitempty"`
	// Replayed marque ce qui a attendu une reconnexion : écrit dans
	// l'histoire, mais pas de quoi bouger l'état ni les compteurs.
	Replayed bool `json:"replayed,omitempty"`
}

// Day est l'agrégat d'un jour UTC : le compte des essais et des succès,
// pas un pourcentage, pour que les fenêtres s'additionnent.
type Day struct {
	ProbeID    string
	Day        time.Time
	Total      int
	Success    int
	Degraded   int
	DurationMs int64
}

// Definition est ce que l'opérateur donne pour créer une sonde.
type Definition struct {
	Name              string
	Kind              Kind
	Target            string
	MachineID         string
	ServiceID         string
	Interval          time.Duration
	Timeout           time.Duration
	FailureThreshold  int
	RecoveryThreshold int
	Method            string
	ExpectedStatus    string
	ExpectedBody      string
	FollowRedirects   bool
	TLS               bool
}

var (
	ErrNotFound         = errors.New("probe not found")
	ErrNameInvalid      = errors.New("probe name must be 1 to 80 characters")
	ErrKindInvalid      = errors.New("probe kind must be http or tcp")
	ErrTargetInvalid    = errors.New("probe target is not a valid url or host:port")
	ErrTargetForbidden  = errors.New("probe target is a link-local address")
	ErrMachineInvalid   = errors.New("probe machine is required")
	ErrIntervalInvalid  = errors.New("probe interval must be between 30 seconds and 24 hours")
	ErrTimeoutInvalid   = errors.New("probe timeout must be between 1 second and the interval")
	ErrThresholdInvalid = errors.New("probe thresholds must be between 1 and 10")
	ErrMethodInvalid    = errors.New("probe method must be GET, HEAD or POST")
	ErrStatusInvalid    = errors.New("probe expected status must be codes or families")
	ErrBodyInvalid      = errors.New("probe expected body is too long")
	ErrTooMany          = errors.New("this machine already carries the maximum number of probes")
	ErrNotPaused        = errors.New("probe is not paused")
	ErrReportInvalid    = errors.New("probe report out of range")
)

// Complete pose ce que l'opérateur n'a pas dit : les valeurs par défaut,
// et rien pour une sonde TCP, qui n'a ni méthode ni code attendu.
func (d Definition) Complete() Definition {
	d.Name = strings.TrimSpace(d.Name)
	d.Target = strings.TrimSpace(d.Target)
	if d.Interval == 0 {
		d.Interval = DefaultInterval
	}
	if d.Timeout == 0 {
		d.Timeout = DefaultTimeout
	}
	if d.FailureThreshold == 0 {
		d.FailureThreshold = DefaultFailureThreshold
	}
	if d.RecoveryThreshold == 0 {
		d.RecoveryThreshold = DefaultRecoveryThreshold
	}
	if d.Kind != KindHTTP {
		return Definition{
			Name: d.Name, Kind: d.Kind, Target: d.Target, MachineID: d.MachineID, ServiceID: d.ServiceID,
			Interval: d.Interval, Timeout: d.Timeout,
			FailureThreshold: d.FailureThreshold, RecoveryThreshold: d.RecoveryThreshold,
			TLS: d.TLS,
		}
	}
	// L'URL d'une sonde HTTP dit déjà le protocole ; le drapeau ne vaut
	// que pour un port que rien d'autre ne décrit.
	d.TLS = false
	d.Method = strings.ToUpper(strings.TrimSpace(d.Method))
	if d.Method == "" {
		d.Method = "GET"
	}
	if strings.TrimSpace(d.ExpectedStatus) == "" {
		d.ExpectedStatus = "2xx"
	}
	d.ExpectedBody = strings.TrimSpace(d.ExpectedBody)
	return d
}

func (d Definition) Validate() error {
	if d.Name == "" || utf8.RuneCountInString(d.Name) > MaxNameLength {
		return ErrNameInvalid
	}
	if d.Kind != KindHTTP && d.Kind != KindTCP {
		return ErrKindInvalid
	}
	if d.MachineID == "" {
		return ErrMachineInvalid
	}
	if err := validateTarget(d.Kind, d.Target); err != nil {
		return err
	}
	if d.Interval < MinInterval || d.Interval > MaxInterval {
		return ErrIntervalInvalid
	}
	if d.Timeout < MinTimeout || d.Timeout > MaxTimeout || d.Timeout > d.Interval {
		return ErrTimeoutInvalid
	}
	if d.FailureThreshold < 1 || d.FailureThreshold > MaxThreshold || d.RecoveryThreshold < 1 || d.RecoveryThreshold > MaxThreshold {
		return ErrThresholdInvalid
	}
	if d.Kind != KindHTTP {
		return nil
	}
	// Une sonde n'écrit pas : seules les méthodes sans effet attendu sont
	// ouvertes, plus POST que réclament certains points de santé.
	switch d.Method {
	case "GET", "HEAD", "POST":
	default:
		return ErrMethodInvalid
	}
	if !isStatusPattern(d.ExpectedStatus) {
		return ErrStatusInvalid
	}
	if utf8.RuneCountInString(d.ExpectedBody) > MaxBodyLength {
		return ErrBodyInvalid
	}
	return nil
}

const idBytes = 8

// NewID tire l'identifiant d'une sonde, celui des adresses de l'interface.
func NewID() (string, error) {
	raw := make([]byte, idBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}
