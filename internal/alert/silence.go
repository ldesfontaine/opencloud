package alert

import (
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxSilence       = 7 * 24 * time.Hour
	MaxReasonLength  = 200
	maxSilencesShown = 200
)

// Silence tait, pendant une fenêtre, les alertes d'un objet, d'un type, ou
// d'un type sur un objet. Une règle sans filtre est refusée : « tout faire
// taire » se fait canal par canal, en le désactivant. Un objet de genre
// machine tait aussi ce qui vit sur elle.
type Silence struct {
	ID        int64
	Kind      Kind
	Object    Object
	Reason    string
	StartsAt  time.Time
	EndsAt    time.Time
	CreatedAt time.Time
}

// SilenceDefinition est ce que l'opérateur donne.
type SilenceDefinition struct {
	Kind     Kind
	Object   Object
	Reason   string
	Duration time.Duration
}

func (d SilenceDefinition) Validate() error {
	if d.Kind == "" && d.Object.ID == "" {
		return ErrSilenceInvalid
	}
	if d.Kind != "" && !isKind(d.Kind) {
		return ErrSilenceInvalid
	}
	if d.Object.ID != "" && !isObjectKind(d.Object.Kind) {
		return ErrSilenceInvalid
	}
	if d.Duration <= 0 || d.Duration > MaxSilence {
		return ErrSilenceDurationInvalid
	}
	reason := strings.TrimSpace(d.Reason)
	if !utf8.ValidString(reason) || utf8.RuneCountInString(reason) > MaxReasonLength {
		return ErrSilenceInvalid
	}
	return nil
}

func (s Silence) IsActive(now time.Time) bool {
	return !now.Before(s.StartsAt) && now.Before(s.EndsAt)
}

// Matches dit si le silence couvre ce fait à cet instant.
func (s Silence) Matches(fact Fact, now time.Time) bool {
	if !s.IsActive(now) {
		return false
	}
	if s.Kind != "" && s.Kind != fact.Kind {
		return false
	}
	if s.Object.ID == "" {
		return true
	}
	if s.Object.Kind == fact.Object.Kind && s.Object.ID == fact.Object.ID {
		return true
	}
	return s.Object.Kind == ObjectMachine && s.Object.ID == fact.MachineID
}
