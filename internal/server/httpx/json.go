package httpx

import (
	"encoding/json"
	"net/http"
	"time"
)

// WriteJSON écrit la réponse en JSON. L'erreur d'encodage revient à
// l'appelant, qui la journalise : la réponse est déjà partie.
func WriteJSON(w http.ResponseWriter, status int, payload any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(payload)
}

// WriteJSONNoStore fait comme WriteJSON et interdit la mise en cache : l'API
// de l'administration et la page publique rendent un état qui change.
func WriteJSONNoStore(w http.ResponseWriter, status int, payload any) error {
	w.Header().Set("Cache-Control", "no-store")
	return WriteJSON(w, status, payload)
}

// ConvertAll convertit chaque élément ; le résultat n'est jamais nul, il
// s'écrit [] quand il n'y a rien.
func ConvertAll[T, J any](items []T, convert func(T) J) []J {
	converted := make([]J, 0, len(items))
	for _, item := range items {
		converted = append(converted, convert(item))
	}
	return converted
}

// TimeOrNil rend null pour un instant zéro : le front n'a pas à connaître
// l'an 1. Tout part en UTC : la réponse ne dépend pas du fuseau de la
// machine openCloud.
func TimeOrNil(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}
	utc := at.UTC()
	return &utc
}
