package alert

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/refusal"
)

const (
	MaxMute = 7 * 24 * time.Hour
	// Une fenêtre se planifie au plus un an à l'avance, comme une maintenance.
	MaxMuteAhead = 365 * 24 * time.Hour
	// Une fenêtre finie reste lisible ce temps-là, comme une alerte résolue,
	// puis la purge l'efface.
	MuteRetention   = 90 * 24 * time.Hour
	MaxReasonLength = 200
	// Le titre d'un silence annoncé est lu par le public, comme celui d'un
	// incident.
	MaxPublicTitleLength = 120
)

// Les refus gardent le mot que l'opérateur lit : « silence ».
var (
	ErrMuteInvalid         = refusal.Invalid("silence.invalid", "alert: mute invalid")
	ErrMuteDurationInvalid = refusal.Invalid("silence.duration_invalid", "alert: mute duration invalid")
	ErrMuteStartInvalid    = refusal.Invalid("silence.start_invalid", "alert: mute start too far ahead")
	ErrMuteTargetUnknown   = refusal.Invalid("silence.target_unknown", "alert: mute target unknown")
	ErrMuteAnnounceInvalid = refusal.Invalid("silence.announce_invalid", "alert: an announced mute needs a public title and a directory target")
)

// TargetKind est ce qu'une fenêtre tait : un objet de l'annuaire, un
// conteneur présent non déclaré, ou, vide, tout.
type TargetKind string

const (
	TargetAll       TargetKind = ""
	TargetMachine   TargetKind = "machine"
	TargetService   TargetKind = "service"
	TargetContainer TargetKind = "container"
	TargetProbe     TargetKind = "probe"
	TargetHeartbeat TargetKind = "heartbeat"
)

func TargetKinds() []TargetKind {
	return []TargetKind{TargetMachine, TargetService, TargetContainer, TargetProbe, TargetHeartbeat}
}

// Target désigne ce qui est tu ; le nom se lit à la lecture, la clé
// étrangère emporte la fenêtre avec son objet.
type Target struct {
	Kind TargetKind
	ID   string
	Name string
}

// Mute tait, pendant une fenêtre, les alertes de sa cible, d'un type ou de
// tous. Il se juge à chaque envoi : une alerte ouverte avant la fenêtre se
// tait aussi, et la fenêtre finie, l'envoi suivant repart.
type Mute struct {
	ID int64
	// Kind : le type tu ; vide, tous.
	Kind      Kind
	Target    Target
	Reason    string
	StartsAt  time.Time
	EndsAt    time.Time
	CreatedBy audit.Actor
	CreatedAt time.Time
	// Un silence annoncé paraît en public comme une maintenance, sous son
	// titre public ; non annoncé, il reste interne.
	Announce    bool
	PublicTitle string
}

// MuteDefinition est ce que l'opérateur donne. Un début zéro ou passé vaut
// maintenant.
type MuteDefinition struct {
	Kind     Kind
	Target   Target
	Reason   string
	StartsAt time.Time
	Duration time.Duration
	// Annoncer exige un titre public et une cible de l'annuaire.
	Announce    bool
	PublicTitle string
}

// Validate vérifie la définition ; now borne le début planifié.
func (d MuteDefinition) Validate(now time.Time) error {
	if d.Kind != "" && !isKind(d.Kind) {
		return ErrMuteInvalid
	}
	if d.Target.Kind == TargetAll && d.Target.ID != "" {
		return ErrMuteInvalid
	}
	if d.Target.Kind != TargetAll && (!slices.Contains(TargetKinds(), d.Target.Kind) || d.Target.ID == "") {
		return ErrMuteInvalid
	}
	if d.Duration < time.Minute || d.Duration > MaxMute {
		return ErrMuteDurationInvalid
	}
	if d.StartsAt.After(now.Add(MaxMuteAhead)) {
		return ErrMuteStartInvalid
	}
	reason := strings.TrimSpace(d.Reason)
	if !utf8.ValidString(reason) || utf8.RuneCountInString(reason) > MaxReasonLength {
		return ErrMuteInvalid
	}
	return d.validateAnnounce()
}

// validateAnnounce : un silence annoncé porte un titre public et vise un
// objet que rien d'autre qu'une page de statut peut relier à un composant ;
// sans l'annonce, pas de titre.
func (d MuteDefinition) validateAnnounce() error {
	title := strings.TrimSpace(d.PublicTitle)
	if !d.Announce {
		if title != "" {
			return ErrMuteAnnounceInvalid
		}
		return nil
	}
	validTitle := title != "" && utf8.ValidString(title) && utf8.RuneCountInString(title) <= MaxPublicTitleLength && !strings.ContainsAny(title, "\n\r")
	if !validTitle || !canAnnounce(d.Target.Kind) {
		return ErrMuteAnnounceInvalid
	}
	return nil
}

// canAnnounce : les genres de l'annuaire que le public sait relier à un
// composant ; ni « tout » ni un conteneur non déclaré.
func canAnnounce(kind TargetKind) bool {
	return kind == TargetMachine || kind == TargetService || kind == TargetProbe || kind == TargetHeartbeat
}

// Window rend la fenêtre de la définition : elle commence au plus tôt
// maintenant.
func (d MuteDefinition) Window(now time.Time) (startsAt, endsAt time.Time) {
	startsAt = now
	if d.StartsAt.After(now) {
		startsAt = d.StartsAt
	}
	return startsAt, startsAt.Add(d.Duration)
}

func (m Mute) IsActive(now time.Time) bool {
	return !now.Before(m.StartsAt) && now.Before(m.EndsAt)
}

// Covers dit si la fenêtre tait cette alerte à cet instant. Une machine tait
// ce qui vit sur elle ; un service déclaré, ses conteneurs et ses sondes.
func (m Mute) Covers(alert Alert, now time.Time) bool {
	if !m.IsActive(now) {
		return false
	}
	if m.Kind != "" && m.Kind != alert.Kind {
		return false
	}
	switch m.Target.Kind {
	case TargetAll:
		return true
	case TargetMachine:
		return alert.MachineID == m.Target.ID
	case TargetService:
		return alert.ServiceID == m.Target.ID
	case TargetContainer:
		return alert.Object.Kind == ObjectContainer && alert.Object.ID == m.Target.ID
	case TargetProbe:
		return alert.Object.Kind == ObjectProbe && alert.Object.ID == m.Target.ID
	case TargetHeartbeat:
		return alert.Object.Kind == ObjectHeartbeat && alert.Object.ID == m.Target.ID
	}
	return false
}

// mutedBy dit si l'une des fenêtres tait l'alerte.
func mutedBy(mutes []Mute, alert Alert, now time.Time) bool {
	for _, mute := range mutes {
		if mute.Covers(alert, now) {
			return true
		}
	}
	return false
}
