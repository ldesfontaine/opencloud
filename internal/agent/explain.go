package agent

import (
	"errors"
	"net/http"

	"github.com/ldesfontaine/opencloud/internal/lang"
)

// Explain traduit ce qui a arrêté l'agent en une phrase pour l'opérateur,
// dans sa langue. Le booléen dit si c'est un refus (rien à réparer côté
// machine, il faut un nouveau jeton) ou une erreur (le détail suit).
func Explain(err error, text lang.Catalog) (string, bool) {
	var refused *ServerError
	switch {
	case errors.Is(err, ErrNotEnrolled):
		return text.Get("agent.not_enrolled"), true
	case errors.Is(err, ErrIdentityRefused):
		return text.Get("agent.identity_refused"), true
	case errors.As(err, &refused):
		if key, ok := refusalKeys[refused.Code]; ok {
			return text.Get(key), true
		}
		if refused.Status >= http.StatusInternalServerError {
			return text.Format("agent.server_failed", refused.Status), false
		}
		return text.Format("agent.failed", err.Error()), false
	default:
		return text.Format("agent.failed", err.Error()), false
	}
}

// Les codes que le serveur renvoie et qui se disent à l'opérateur.
var refusalKeys = map[string]string{ // #nosec G101 -- des codes de refus, pas des secrets.
	"token_not_found":   "agent.token_not_found",
	"token_consumed":    "agent.token_consumed",
	"token_expired":     "agent.token_expired",
	"name_taken":        "agent.name_taken",
	"machine_not_found": "agent.identity_refused",
	"bad_proof":         "agent.identity_refused",
}
