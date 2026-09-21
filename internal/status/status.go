package status

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/probe"
)

// State est ce qu'un visiteur lit d'un composant, et de la page entière :
// Opérationnel, Dégradé, Panne, Maintenance. Quatre mots, pas cinq : un
// composant est une chose pour le visiteur, s'il est en panne il est en
// panne ; la nuance « partielle » se lit ligne par ligne.
type State string

const (
	StateOperational State = "operational"
	StateDegraded    State = "degraded"
	StateDown        State = "down"
	StateMaintenance State = "maintenance"
	// StateUnknown est celui d'un composant dont aucun objet ne compte :
	// caché du public, signalé à l'opérateur.
	StateUnknown State = ""
)

// rank ordonne les états du moins grave au plus grave : le pire l'emporte.
// Une maintenance passe devant un dégradé, jamais devant une panne.
func rank(state State) int {
	switch state {
	case StateDegraded:
		return 1
	case StateMaintenance:
		return 2
	case StateDown:
		return 3
	}
	return 0
}

// Worst rend le pire des états ; Unknown quand la liste est vide.
func Worst(states []State) State {
	worst := StateUnknown
	for _, state := range states {
		if worst == StateUnknown || rank(state) > rank(worst) {
			worst = state
		}
	}
	return worst
}

// Impact est ce qu'un incident ouvert fait afficher à ses composants : il
// remplace l'état dérivé, parce que l'opérateur en sait plus que la sonde.
type Impact string

const (
	ImpactDegraded    Impact = "degraded"
	ImpactDown        Impact = "down"
	ImpactMaintenance Impact = "maintenance"
)

func (i Impact) State() State {
	return State(i)
}

func isImpact(impact Impact) bool {
	switch impact {
	case ImpactDegraded, ImpactDown, ImpactMaintenance:
		return true
	}
	return false
}

// IncidentStatus suit le fil d'un incident : en cours d'analyse, cause
// identifiée, sous surveillance, résolu ; ou, pour une maintenance,
// planifiée, en cours, résolue.
type IncidentStatus string

const (
	StatusScheduled     IncidentStatus = "scheduled"
	StatusInvestigating IncidentStatus = "investigating"
	StatusIdentified    IncidentStatus = "identified"
	StatusMonitoring    IncidentStatus = "monitoring"
	StatusInProgress    IncidentStatus = "in_progress"
	StatusResolved      IncidentStatus = "resolved"
)

// allowedStatus dit si ce statut a un sens pour cet impact : une
// maintenance se planifie et se déroule, un incident s'analyse.
func allowedStatus(impact Impact, status IncidentStatus) bool {
	if status == StatusResolved {
		return true
	}
	if impact == ImpactMaintenance {
		return status == StatusScheduled || status == StatusInProgress
	}
	return status == StatusInvestigating || status == StatusIdentified || status == StatusMonitoring
}

// MemberKind est le genre d'un objet rattaché à un composant. Le certificat
// n'en est pas un : c'est un fait de la sonde, il vient avec elle.
type MemberKind string

const (
	KindMachine   MemberKind = "machine"
	KindService   MemberKind = "service"
	KindHeartbeat MemberKind = "heartbeat"
	KindProbe     MemberKind = "probe"
)

func isKind(kind MemberKind) bool {
	switch kind {
	case KindMachine, KindService, KindHeartbeat, KindProbe:
		return true
	}
	return false
}

// MemberRef désigne un objet à rattacher.
type MemberRef struct {
	Kind MemberKind
	ID   string
}

// Member est un objet rattaché tel que l'opérateur le voit : son nom, et ce
// qu'il apporte au composant. Jamais servi au public.
type Member struct {
	Kind MemberKind
	ID   string
	Name string
	// State est ce que l'objet apporte ; Counts dit s'il apporte quelque
	// chose : un objet nouveau ou en pause ne compte pas.
	State  State
	Counts bool
	// Present est faux quand l'objet n'existe plus ; le lien part avec lui,
	// mais une lecture entre les deux peut le voir.
	Present bool
}

// Component est un composant tel que la base le connaît, avec ses objets.
type Component struct {
	ID        string
	Name      string
	Position  int
	Members   []Member
	CreatedAt time.Time
	// Derived est le pire des objets qui comptent ; Effective est ce que le
	// public voit, l'impact d'un incident ouvert quand il y en a un.
	Derived   State
	Effective State
}

// Incident est ce que l'opérateur a dit, avec son fil et ses composants.
type Incident struct {
	ID     string
	Title  string
	Impact Impact
	Status IncidentStatus
	// La fenêtre d'une maintenance ; zéro pour un incident.
	StartsAt time.Time
	EndsAt   time.Time
	// Les composants touchés : identifiant et nom, le nom seul en public.
	Components []ComponentRef
	Updates    []Update
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ResolvedAt time.Time
}

func (i Incident) IsMaintenance() bool {
	return i.Impact == ImpactMaintenance
}

func (i Incident) IsResolved() bool {
	return i.Status == StatusResolved
}

// Applies dit si l'incident remplace aujourd'hui l'état de ses composants :
// ouvert, et pour une maintenance, commencée.
func (i Incident) Applies() bool {
	return !i.IsResolved() && i.Status != StatusScheduled
}

type ComponentRef struct {
	ID   string
	Name string
}

// Update est une entrée du fil : le statut qu'elle a donné, un message.
type Update struct {
	ID        int64
	Status    IncidentStatus
	Message   string
	CreatedAt time.Time
}

// Page est ce que l'opérateur règle de la page : son titre, une annonce en
// texte brut, sa langue. Un visiteur ne choisit pas la langue.
type Page struct {
	Title        string
	Announcement string
	Language     lang.Code
}

// ComponentDefinition est ce que l'opérateur donne pour créer ou modifier
// un composant.
type ComponentDefinition struct {
	Name     string
	Position int
	Members  []MemberRef
}

// IncidentDefinition est ce que l'opérateur donne pour ouvrir un incident
// ou planifier une maintenance. Message est la première entrée du fil.
type IncidentDefinition struct {
	Title        string
	Impact       Impact
	Status       IncidentStatus
	Message      string
	ComponentIDs []string
	StartsAt     time.Time
	EndsAt       time.Time
}

// IncidentChange est ce que l'opérateur peut corriger après coup : le
// titre, les composants, la fenêtre. Le fil ne se réécrit pas.
type IncidentChange struct {
	Title        string
	ComponentIDs []string
	StartsAt     time.Time
	EndsAt       time.Time
}

const (
	MaxNameLength         = 80
	MaxTitleLength        = 120
	MaxMessageLength      = 2000
	MaxAnnouncementLength = 500
	MaxMembers            = 64
	MaxComponents         = 64
	// Ce que le public voit du passé, et ce que la base garde.
	HistorySpan       = 14 * 24 * time.Hour
	IncidentRetention = 365 * 24 * time.Hour
	// Les jours d'uptime servis par composant, comme la fiche d'une sonde.
	UptimeSpan = 90 * 24 * time.Hour
	// Une maintenance ne se planifie pas plus d'un an à l'avance, ni plus
	// longue qu'une semaine : au-delà, c'est une erreur de saisie.
	MaxScheduleAhead = 365 * 24 * time.Hour
	MaxWindow        = 7 * 24 * time.Hour
	idBytes          = 8
)

var (
	ErrNotFound            = errors.New("status: not found")
	ErrNameInvalid         = errors.New("status: name invalid")
	ErrTitleInvalid        = errors.New("status: title invalid")
	ErrPageTitleInvalid    = errors.New("status: page title invalid")
	ErrMessageInvalid      = errors.New("status: message invalid")
	ErrAnnouncementInvalid = errors.New("status: announcement invalid")
	ErrLanguageInvalid     = errors.New("status: language invalid")
	ErrImpactInvalid       = errors.New("status: impact invalid")
	ErrStatusInvalid       = errors.New("status: status invalid")
	ErrMemberInvalid       = errors.New("status: member invalid")
	ErrComponentInvalid    = errors.New("status: component invalid")
	ErrWindowInvalid       = errors.New("status: window invalid")
	ErrTooMany             = errors.New("status: too many")
	ErrResolved            = errors.New("status: incident resolved")
)

func (d ComponentDefinition) Validate() error {
	if !validText(d.Name, MaxNameLength) {
		return ErrNameInvalid
	}
	if len(d.Members) > MaxMembers {
		return ErrTooMany
	}
	seen := make(map[MemberRef]bool, len(d.Members))
	for _, member := range d.Members {
		if !isKind(member.Kind) || member.ID == "" || seen[member] {
			return ErrMemberInvalid
		}
		seen[member] = true
	}
	return nil
}

// Validate vérifie la définition avant l'écriture ; now sert à borner la
// fenêtre d'une maintenance.
func (d IncidentDefinition) Validate(now time.Time) error {
	if !validText(d.Title, MaxTitleLength) {
		return ErrTitleInvalid
	}
	if !isImpact(d.Impact) {
		return ErrImpactInvalid
	}
	if !allowedStatus(d.Impact, d.Status) || d.Status == StatusResolved {
		return ErrStatusInvalid
	}
	if !validMessage(d.Message) {
		return ErrMessageInvalid
	}
	if len(d.ComponentIDs) == 0 || len(d.ComponentIDs) > MaxComponents {
		return ErrComponentInvalid
	}
	return validateWindow(d.Impact, d.StartsAt, d.EndsAt, now)
}

// Une maintenance porte une fenêtre : un début, une fin après lui, ni trop
// loin ni trop longue. Un incident n'en a pas.
func validateWindow(impact Impact, startsAt, endsAt, now time.Time) error {
	if impact != ImpactMaintenance {
		if !startsAt.IsZero() || !endsAt.IsZero() {
			return ErrWindowInvalid
		}
		return nil
	}
	if startsAt.IsZero() || endsAt.IsZero() || !endsAt.After(startsAt) {
		return ErrWindowInvalid
	}
	if startsAt.After(now.Add(MaxScheduleAhead)) || endsAt.Sub(startsAt) > MaxWindow {
		return ErrWindowInvalid
	}
	return nil
}

// Validate tolère un titre vide : la page porte alors celui du catalogue.
func (p Page) Validate() error {
	if strings.TrimSpace(p.Title) != "" && !validText(p.Title, MaxTitleLength) {
		return ErrPageTitleInvalid
	}
	if utf8.RuneCountInString(p.Announcement) > MaxAnnouncementLength || !utf8.ValidString(p.Announcement) {
		return ErrAnnouncementInvalid
	}
	if p.Language != "" {
		if _, ok := lang.Parse(string(p.Language)); !ok {
			return ErrLanguageInvalid
		}
	}
	return nil
}

func validText(value string, max int) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed != "" && utf8.ValidString(trimmed) && utf8.RuneCountInString(trimmed) <= max && !strings.ContainsAny(trimmed, "\n\r")
}

func validMessage(message string) bool {
	return utf8.ValidString(message) && utf8.RuneCountInString(message) <= MaxMessageLength
}

// NewID tire l'identifiant d'un composant ou d'un incident : il sort en
// public, il ne doit rien dire.
func NewID() (string, error) {
	raw := make([]byte, idBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// Day est l'uptime d'un composant pour un jour UTC : la somme des jours de
// ses sondes. Des comptes, jamais un pourcentage.
type Day struct {
	Day      time.Time
	Total    int
	Success  int
	Degraded int
}

// Snapshot est ce que le public voit, et rien de plus : ni un identifiant
// d'objet, ni un nom de machine, de conteneur ou de sonde, ni une cible.
type Snapshot struct {
	Page        Page
	GeneratedAt time.Time
	// Global est le pire des composants visibles ; Unknown sans composant.
	Global     State
	Components []PublicComponent
	// Open : les incidents ouverts et les maintenances planifiées ;
	// History : ce qui a été résolu dans les quatorze derniers jours.
	Open    []Incident
	History []Incident
}

type PublicComponent struct {
	ID    string
	Name  string
	State State
	// Days est vide pour un composant sans sonde : pas de barre.
	Days []Day
}

// certificateDegrades dit si le certificat vu par une sonde en ligne la
// rend dégradée pour le public : à renouveler ou expiré.
func certificateDegrades(certificate *probe.Certificate, now time.Time) bool {
	if certificate == nil {
		return false
	}
	return certificate.NotAfter.Before(now.Add(probe.CertificateWarning * 24 * time.Hour))
}
