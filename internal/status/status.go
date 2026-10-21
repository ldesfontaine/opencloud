package status

import (
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ldesfontaine/opencloud/internal/lang"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/refusal"
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

// States liste les états qui se disent ; Unknown, celui d'un composant
// caché, n'en est pas un.
func States() []State {
	return []State{StateOperational, StateDegraded, StateDown, StateMaintenance}
}

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

func Impacts() []Impact {
	return []Impact{ImpactDegraded, ImpactDown, ImpactMaintenance}
}

func isImpact(impact Impact) bool {
	return slices.Contains(Impacts(), impact)
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

// IncidentStatuses liste les statuts, dans l'ordre où l'opérateur les
// parcourt.
func IncidentStatuses() []IncidentStatus {
	return []IncidentStatus{StatusScheduled, StatusInvestigating, StatusIdentified, StatusMonitoring, StatusInProgress, StatusResolved}
}

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

func MemberKinds() []MemberKind {
	return []MemberKind{KindMachine, KindService, KindHeartbeat, KindProbe}
}

func isKind(kind MemberKind) bool {
	return slices.Contains(MemberKinds(), kind)
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

// Component est un composant tel que la base le connaît, avec ses objets ;
// il appartient à une page.
type Component struct {
	ID        string
	PageID    string
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

// Page est une page de statut : son adresse sous /statut/, son titre, une
// annonce en texte brut, sa langue, et le projet qu'elle montre s'il y en a
// un. Un visiteur ne choisit pas la langue ; vide, c'est celle de
// l'interface, résolue à la lecture.
type Page struct {
	ID           string
	ProjectID    string
	Slug         string
	Title        string
	Announcement string
	Language     lang.Code
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// PageDefinition est ce que l'opérateur donne pour créer ou modifier une
// page. Un projet vide : la page n'est rangée nulle part.
type PageDefinition struct {
	Slug         string
	Title        string
	Announcement string
	Language     lang.Code
	ProjectID    string
}

// ComponentDefinition est ce que l'opérateur donne pour créer ou modifier
// un composant. La page se choisit à la création et ne change plus.
type ComponentDefinition struct {
	PageID   string
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
	// Par page : la page publique reste lisible, et l'instantané borné.
	MaxComponents = 64
	// Les pages se créent à la main ; chacune se relit à chaque changement.
	MaxPages      = 64
	MaxSlugLength = 63
	// Ce que le public voit du passé, et ce que la base garde.
	HistorySpan       = 14 * 24 * time.Hour
	IncidentRetention = 365 * 24 * time.Hour
	// Les jours d'uptime servis par composant, comme la fiche d'une sonde.
	UptimeSpan = 90 * 24 * time.Hour
	// Une maintenance ne se planifie pas plus d'un an à l'avance, ni plus
	// longue qu'une semaine : au-delà, c'est une erreur de saisie.
	MaxScheduleAhead = 365 * 24 * time.Hour
	MaxWindow        = 7 * 24 * time.Hour
)

var (
	ErrNotFound            = errors.New("status: not found")
	ErrNameInvalid         = refusal.Invalid("component.name_invalid", "status: name invalid")
	ErrTitleInvalid        = refusal.Invalid("incident.title_invalid", "status: title invalid")
	ErrPageTitleInvalid    = refusal.Invalid("statuspage.title_invalid", "status: page title invalid")
	ErrMessageInvalid      = refusal.Invalid("incident.message_invalid", "status: message invalid")
	ErrAnnouncementInvalid = refusal.Invalid("statuspage.announcement_invalid", "status: announcement invalid")
	ErrLanguageInvalid     = refusal.Invalid("statuspage.language_invalid", "status: language invalid")
	ErrImpactInvalid       = refusal.Invalid("incident.impact_invalid", "status: impact invalid")
	ErrStatusInvalid       = refusal.Invalid("incident.status_invalid", "status: status invalid")
	ErrMemberInvalid       = refusal.Invalid("component.member_invalid", "status: member invalid")
	ErrComponentInvalid    = refusal.Invalid("incident.component_invalid", "status: component invalid")
	ErrWindowInvalid       = refusal.Invalid("incident.window_invalid", "status: window invalid")
	ErrTooMany             = refusal.Invalid("component.too_many", "status: too many")
	ErrPageInvalid         = refusal.Invalid("component.page_invalid", "status: component page unknown")
	ErrSlugInvalid         = refusal.Invalid("statuspage.slug_invalid", "status: page slug invalid")
	ErrSlugTaken           = refusal.Invalid("statuspage.slug_taken", "status: page slug already used")
	ErrProjectInvalid      = refusal.Invalid("statuspage.project_invalid", "status: page project unknown")
	ErrProjectTaken        = refusal.Invalid("statuspage.project_taken", "status: project already has a page")
	ErrTooManyPages        = refusal.Invalid("statuspage.too_many", "status: too many pages")
	ErrResolved            = refusal.Conflict("incident.already_resolved", "status: incident resolved")
	ErrUpdateEmpty         = refusal.Invalid("incident.update_empty", "status: update says nothing")
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
	if err := validateWindow(d.Impact, d.StartsAt, d.EndsAt, now); err != nil {
		return err
	}
	// Une maintenance déjà finie n'a rien à annoncer : publiée, elle
	// s'ouvrirait puis se résoudrait aussitôt, datée d'aujourd'hui.
	if d.Impact == ImpactMaintenance && !d.EndsAt.After(now) {
		return ErrWindowInvalid
	}
	return nil
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
func (d PageDefinition) Validate() error {
	if !validSlug(d.Slug) {
		return ErrSlugInvalid
	}
	if strings.TrimSpace(d.Title) != "" && !validText(d.Title, MaxTitleLength) {
		return ErrPageTitleInvalid
	}
	if utf8.RuneCountInString(d.Announcement) > MaxAnnouncementLength || !utf8.ValidString(d.Announcement) {
		return ErrAnnouncementInvalid
	}
	if d.Language != "" {
		if _, ok := lang.Parse(string(d.Language)); !ok {
			return ErrLanguageInvalid
		}
	}
	return nil
}

// reservedSlugs ne nomment pas une page : « api » a été le chemin de l'API
// publique sous /statut, un lien ancien ne doit pas tomber sur une page.
var reservedSlugs = []string{"api"}

// validSlug : minuscules, chiffres et tirets, commençant par une lettre ou
// un chiffre ; un seul segment d'URL, rien à échapper.
func validSlug(slug string) bool {
	if slug == "" || len(slug) > MaxSlugLength || slices.Contains(reservedSlugs, slug) {
		return false
	}
	for index, char := range slug {
		isLower := char >= 'a' && char <= 'z'
		isDigit := char >= '0' && char <= '9'
		if !isLower && !isDigit && (char != '-' || index == 0) {
			return false
		}
	}
	return true
}

func validText(value string, max int) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed != "" && utf8.ValidString(trimmed) && utf8.RuneCountInString(trimmed) <= max && !strings.ContainsAny(trimmed, "\n\r")
}

func validMessage(message string) bool {
	return utf8.ValidString(message) && utf8.RuneCountInString(message) <= MaxMessageLength
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
