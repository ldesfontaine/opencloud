package systemdunits

import (
	"errors"
	"regexp"
	"slices"
)

// Les bornes d'un rapport : l'agent les applique en lisant, le serveur les
// vérifie en recevant (un pair peut mentir).
const (
	MaxUnits    = 512
	MaxNameSize = 255
)

// ErrReportInvalid : le rapport sort de ce qu'un agent peut avoir relevé. Il
// est refusé en entier.
var ErrReportInvalid = errors.New("systemd units report out of range")

// namePattern est celui des unités que l'annuaire accepte de déclarer
// (directory.systemdUnitPattern) : une unité de service, sans caractère
// d'échappement. Une unité dont le nom en porte un (instance d'un chemin) ne
// peut pas être déclarée, elle n'est donc pas relevée.
var namePattern = regexp.MustCompile(`^[A-Za-z0-9:_.@-]{1,250}\.service$`)

// IsUnitName dit si un nom est celui d'une unité de service relevable.
func IsUnitName(name string) bool {
	return len(name) <= MaxNameSize && namePattern.MatchString(name)
}

// Other : une valeur que la liste fermée ne connaît pas. Ce que systemd
// écrit est libre ; seul un mot de la liste est rapporté, jamais l'inconnu.
const Other = "other"

// Load est l'état de chargement d'une unité. « not-found » n'est pas relevé :
// systemd en liste pour toute dépendance citée, ce n'est pas une unité de la
// machine.
type Load string

const (
	LoadLoaded Load = "loaded"
	LoadMasked Load = "masked"
	LoadError  Load = "error"
)

func Loads() []Load {
	return []Load{LoadLoaded, LoadMasked, LoadError, Other}
}

// Active est l'état d'activité d'une unité.
type Active string

const (
	ActiveActive       Active = "active"
	ActiveInactive     Active = "inactive"
	ActiveFailed       Active = "failed"
	ActiveActivating   Active = "activating"
	ActiveDeactivating Active = "deactivating"
	ActiveReloading    Active = "reloading"
)

func Actives() []Active {
	return []Active{ActiveActive, ActiveInactive, ActiveFailed, ActiveActivating, ActiveDeactivating, ActiveReloading, Other}
}

// Sub est le sous-état d'un service (systemd.service(5)) ; le mot que
// systemd écrit, quand il est de la liste.
type Sub string

const (
	SubRunning     Sub = "running"
	SubExited      Sub = "exited"
	SubDead        Sub = "dead"
	SubFailed      Sub = "failed"
	SubAutoRestart Sub = "auto-restart"
	SubStart       Sub = "start"
	SubStop        Sub = "stop"
	SubReload      Sub = "reload"
)

func Subs() []Sub {
	return []Sub{SubRunning, SubExited, SubDead, SubFailed, SubAutoRestart, SubStart, SubStop, SubReload, Other}
}

// Reason dit pourquoi une machine n'a pas pu être relevée.
type Reason string

const (
	// ReasonNoSystemctl : la commande manque (une machine sans systemd).
	ReasonNoSystemctl Reason = "no_systemctl"
	// ReasonNotBooted : systemctl répond que systemd n'est pas le système
	// d'amorçage (un conteneur, un chroot).
	ReasonNotBooted Reason = "not_booted"
	// ReasonUnreadable : la lecture a échoué pour une autre raison.
	ReasonUnreadable Reason = "unreadable"
)

func Reasons() []Reason {
	return []Reason{ReasonNoSystemctl, ReasonNotBooted, ReasonUnreadable}
}

// Unit est une unité de service : son nom et ses états, rien d'autre.
type Unit struct {
	Name   string `json:"name"`
	Load   Load   `json:"load"`
	Active Active `json:"active"`
	Sub    Sub    `json:"sub"`
}

// IsRunning : l'unité tourne (active, sous-état running ou exited).
func (u Unit) IsRunning() bool {
	return u.Active == ActiveActive
}

// Report est le relevé d'une machine, remplacé à chaque fois. Available dit
// que systemd a pu être lu ; sinon Reason le dit et Units est vide.
type Report struct {
	Available bool   `json:"available"`
	Reason    Reason `json:"reason,omitempty"`
	// Truncated : la machine a plus de MaxUnits unités, les suivantes (dans
	// l'ordre des noms) n'y sont pas.
	Truncated bool   `json:"truncated,omitempty"`
	Units     []Unit `json:"units"`
}

// Validate refuse en bloc un rapport hors de ce qu'un agent peut avoir relevé.
func (r Report) Validate() error {
	if !r.Available {
		if !slices.Contains(Reasons(), r.Reason) || len(r.Units) > 0 || r.Truncated {
			return ErrReportInvalid
		}
		return nil
	}
	if r.Reason != "" || len(r.Units) > MaxUnits {
		return ErrReportInvalid
	}
	seen := make(map[string]bool, len(r.Units))
	for _, unit := range r.Units {
		if !IsUnitName(unit.Name) || seen[unit.Name] {
			return ErrReportInvalid
		}
		seen[unit.Name] = true
		if !slices.Contains(Loads(), unit.Load) || !slices.Contains(Actives(), unit.Active) || !slices.Contains(Subs(), unit.Sub) {
			return ErrReportInvalid
		}
	}
	return nil
}
