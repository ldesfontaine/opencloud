package apitoken

import (
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ldesfontaine/opencloud/internal/refusal"
	"github.com/ldesfontaine/opencloud/internal/verb"
)

const (
	MaxTokens     = 16
	MaxNameLength = 80
	// L'échéance d'un jeton quand la demande n'en dit rien.
	DefaultDays = 90
	// La purge efface les jetons échus.
	PurgeEvery = 15 * time.Minute
)

// Lifetimes sont les durées de vie offertes à un jeton, en jours ; 0 veut
// dire sans échéance, un choix explicite.
func Lifetimes() []int {
	return []int{30, 90, 365, 0}
}

// Scope est ce qu'un jeton peut faire.
type Scope string

const (
	ScopeRead Scope = "read"
	// Lit comme read, et ne déclenche que les verbes que le jeton porte
	// (internal/verb) : sans verbe, il n'agit sur rien.
	ScopeWrite Scope = "write"
)

// CreatableScopes sont les portées que la création accepte.
func CreatableScopes() []Scope {
	return []Scope{ScopeRead, ScopeWrite}
}

// Token est un jeton stocké haché. ExpiresAt est zéro pour un jeton créé
// sans échéance.
type Token struct {
	ID         string
	Hash       string
	Prefix     string
	Scope      Scope
	Name       string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastUsedAt time.Time
	// Les verbes que le jeton peut déclencher, triés ; vide pour un jeton
	// read, et pour un jeton write que l'opérateur n'a rien coché.
	Verbs []string
}

// CanDo dit si le jeton porte ce verbe. Seule une portée write en porte.
func (t Token) CanDo(verbID string) bool {
	return t.Scope == ScopeWrite && slices.Contains(t.Verbs, verbID)
}

func (t Token) IsExpired(now time.Time) bool {
	return !t.ExpiresAt.IsZero() && !now.Before(t.ExpiresAt)
}

// Masked est tout ce qu'une page peut montrer d'un jeton.
func (t Token) Masked() string {
	return t.Prefix + "…"
}

// Request est ce que l'opérateur choisit en créant un jeton. Une portée
// vide vaut read ; Days nil vaut DefaultDays.
type Request struct {
	Name  string
	Scope Scope
	Days  *int
	// Les verbes cochés : réservés à la portée write, tous connus du
	// registre.
	Verbs []string
}

var (
	ErrNotFound = errors.New("api token: not found")
	// Inconnu, révoqué (donc effacé) ou échu : le même refus, rien ne dit à
	// qui présente un jeton ce qu'il a deviné.
	ErrInvalid         = errors.New("api token: invalid")
	ErrNameInvalid     = refusal.Invalid("api_token.name_invalid", "api token: name invalid")
	ErrTooMany         = refusal.Invalid("api_token.too_many", "api token: too many tokens")
	ErrScopeInvalid    = refusal.Invalid("api_token.scope_invalid", "api token: scope invalid")
	ErrLifetimeInvalid = refusal.Invalid("api_token.lifetime_invalid", "api token: lifetime invalid")
	ErrVerbUnknown     = refusal.Invalid("api_token.verb_unknown", "api token: verb unknown")
	ErrVerbsNeedWrite  = refusal.Invalid("api_token.verbs_need_write", "api token: verbs need the write scope")
)

// verbsOf juge les verbes cochés : connus, sans doublon, triés. Une portée
// read n'en porte aucun, et le dit plutôt que de les ignorer en silence.
func verbsOf(scope Scope, requested []string) ([]string, error) {
	if len(requested) == 0 {
		return nil, nil
	}
	if scope != ScopeWrite {
		return nil, ErrVerbsNeedWrite
	}
	verbs := make([]string, 0, len(requested))
	for _, id := range requested {
		if _, known := verb.Default.Lookup(id); !known {
			return nil, ErrVerbUnknown
		}
		if !slices.Contains(verbs, id) {
			verbs = append(verbs, id)
		}
	}
	slices.Sort(verbs)
	return verbs, nil
}

// scopeOf lit la portée demandée : vide vaut lecture seule.
func scopeOf(requested Scope) (Scope, error) {
	if requested == "" {
		return ScopeRead, nil
	}
	if slices.Contains(CreatableScopes(), requested) {
		return requested, nil
	}
	return "", ErrScopeInvalid
}

// expiryOf rend l'échéance d'un jeton créé maintenant : zéro pour 0 jour,
// sans échéance ; une durée hors de la liste est refusée.
func expiryOf(requested *int, now time.Time) (time.Time, error) {
	days := DefaultDays
	if requested != nil {
		days = *requested
	}
	if !slices.Contains(Lifetimes(), days) {
		return time.Time{}, ErrLifetimeInvalid
	}
	if days == 0 {
		return time.Time{}, nil
	}
	// Des jours de 24 h, pas des jours du calendrier local : un passage à
	// l'heure d'hiver ne décale pas l'échéance.
	return now.Add(time.Duration(days) * 24 * time.Hour), nil
}

// ValidateName borne le nom d'un jeton : un mot pour le reconnaître.
func ValidateName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || utf8.RuneCountInString(trimmed) > MaxNameLength {
		return ErrNameInvalid
	}
	return nil
}
