package verb

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Verb est un geste que l'opérateur peut cocher sur un jeton. Route est le
// motif enregistré de la route d'opérateur qu'il ouvre, « POST /api/... » ;
// Label et Summary sont des clés de catalogue (fr et en). Reverse est le
// verbe qui défait celui-ci : obligatoire, un verbe sans inverse reste
// réservé à l'opérateur.
type Verb struct {
	ID      string
	Route   string
	Label   string
	Summary string
	Reverse string
}

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

var (
	ErrIDInvalid      = errors.New("verb: id invalid")
	ErrRouteInvalid   = errors.New("verb: route must be a POST on /api/")
	ErrCatalogMissing = errors.New("verb: label and summary keys are required")
	ErrNoReverse      = errors.New("verb: a verb without a reverse cannot be registered")
	ErrDuplicate      = errors.New("verb: already registered")
	ErrUnknownReverse = errors.New("verb: reverse is not registered")
)

// Registry est la liste des verbes. L'ordre d'enregistrement est celui de
// l'interface.
type Registry struct {
	verbs []Verb
}

// Register ajoute un verbe. Il ne vérifie pas que l'inverse existe déjà :
// deux verbes se répondent, l'un des deux vient forcément en premier. C'est
// Check qui le tranche, une fois tout enregistré.
func (r *Registry) Register(v Verb) error {
	if !idPattern.MatchString(v.ID) {
		return fmt.Errorf("%w: %q", ErrIDInvalid, v.ID)
	}
	if !strings.HasPrefix(v.Route, "POST /api/") {
		return fmt.Errorf("%w: %q", ErrRouteInvalid, v.Route)
	}
	if v.Label == "" || v.Summary == "" {
		return fmt.Errorf("%w: %s", ErrCatalogMissing, v.ID)
	}
	if v.Reverse == "" {
		return fmt.Errorf("%w: %s", ErrNoReverse, v.ID)
	}
	if _, found := r.Lookup(v.ID); found {
		return fmt.Errorf("%w: %s", ErrDuplicate, v.ID)
	}
	if _, found := r.ByRoute(v.Route); found {
		return fmt.Errorf("%w: route %s", ErrDuplicate, v.Route)
	}
	r.verbs = append(r.verbs, v)
	return nil
}

// MustRegister est Register pour le câblage au démarrage : une erreur est
// un défaut du code, pas une entrée à refuser.
func (r *Registry) MustRegister(v Verb) {
	if err := r.Register(v); err != nil {
		panic(err)
	}
}

// Check vérifie que chaque inverse est un verbe enregistré.
func (r *Registry) Check() error {
	for _, v := range r.verbs {
		if _, found := r.Lookup(v.Reverse); !found {
			return fmt.Errorf("%w: %s -> %s", ErrUnknownReverse, v.ID, v.Reverse)
		}
	}
	return nil
}

func (r *Registry) Lookup(id string) (Verb, bool) {
	index := slices.IndexFunc(r.verbs, func(v Verb) bool { return v.ID == id })
	if index < 0 {
		return Verb{}, false
	}
	return r.verbs[index], true
}

// ByRoute rend le verbe qui ouvre cette route d'opérateur.
func (r *Registry) ByRoute(route string) (Verb, bool) {
	index := slices.IndexFunc(r.verbs, func(v Verb) bool { return v.Route == route })
	if index < 0 {
		return Verb{}, false
	}
	return r.verbs[index], true
}

// All rend une copie, dans l'ordre d'enregistrement.
func (r *Registry) All() []Verb {
	return slices.Clone(r.verbs)
}
