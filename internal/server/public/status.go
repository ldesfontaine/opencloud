package public

import (
	"net/http"
	"time"

	"github.com/ldesfontaine/opencloud/internal/server/httpx"
	"github.com/ldesfontaine/opencloud/internal/status"
)

// Ce que le public lit : le titre, l'annonce, la langue, l'état global,
// les composants et les incidents. Jamais un identifiant d'objet, un nom
// de machine, de conteneur ou de sonde, une cible, un port. Un test le
// prouve sur la réponse réelle.
type publicStatusJSON struct {
	Title        string                `json:"title"`
	Announcement string                `json:"announcement"`
	Language     string                `json:"language"`
	GeneratedAt  time.Time             `json:"generated_at"`
	Global       string                `json:"global"`
	Components   []publicComponentJSON `json:"components"`
	Open         []publicIncidentJSON  `json:"open"`
	History      []publicIncidentJSON  `json:"history"`
}

type publicComponentJSON struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	State string          `json:"state"`
	Days  []statusDayJSON `json:"days"`
}

// Un jour d'uptime d'un composant : des comptes, jamais un pourcentage.
type statusDayJSON struct {
	Day      string `json:"day"`
	Total    int    `json:"total"`
	Success  int    `json:"success"`
	Degraded int    `json:"degraded"`
}

type publicIncidentJSON struct {
	ID         string       `json:"id"`
	Title      string       `json:"title"`
	Impact     string       `json:"impact"`
	Status     string       `json:"status"`
	StartsAt   *time.Time   `json:"starts_at"`
	EndsAt     *time.Time   `json:"ends_at"`
	Components []string     `json:"components"`
	Updates    []updateJSON `json:"updates"`
	CreatedAt  time.Time    `json:"created_at"`
	UpdatedAt  time.Time    `json:"updated_at"`
	ResolvedAt *time.Time   `json:"resolved_at"`
}

type updateJSON struct {
	Status    string    `json:"status"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

// GET /statut/api/status
func (h *Handler) serveStatus(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.status.Snapshot(r.Context())
	if err != nil {
		h.logger.Error("status snapshot", "error", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	h.writeJSON(w, http.StatusOK, snapshotToJSON(snapshot))
}

func snapshotToJSON(snapshot status.Snapshot) publicStatusJSON {
	response := publicStatusJSON{
		Title:        snapshot.Page.Title,
		Announcement: snapshot.Page.Announcement,
		Language:     string(snapshot.Page.Language),
		GeneratedAt:  snapshot.GeneratedAt.UTC(),
		Global:       string(snapshot.Global),
		Components:   make([]publicComponentJSON, 0, len(snapshot.Components)),
		Open:         httpx.ConvertAll(snapshot.Open, publicIncidentToJSON),
		History:      httpx.ConvertAll(snapshot.History, publicIncidentToJSON),
	}
	for _, component := range snapshot.Components {
		days := httpx.ConvertAll(component.Days, func(day status.Day) statusDayJSON {
			return statusDayJSON{Day: day.Day.UTC().Format(time.DateOnly), Total: day.Total, Success: day.Success, Degraded: day.Degraded}
		})
		response.Components = append(response.Components, publicComponentJSON{ID: component.ID, Name: component.Name, State: string(component.State), Days: days})
	}
	return response
}

func publicIncidentToJSON(incident status.Incident) publicIncidentJSON {
	return publicIncidentJSON{
		ID:         incident.ID,
		Title:      incident.Title,
		Impact:     string(incident.Impact),
		Status:     string(incident.Status),
		StartsAt:   httpx.TimeOrNil(incident.StartsAt),
		EndsAt:     httpx.TimeOrNil(incident.EndsAt),
		Components: httpx.ConvertAll(incident.Components, func(component status.ComponentRef) string { return component.Name }),
		Updates:    updatesToJSON(incident.Updates),
		CreatedAt:  incident.CreatedAt.UTC(),
		UpdatedAt:  incident.UpdatedAt.UTC(),
		ResolvedAt: httpx.TimeOrNil(incident.ResolvedAt),
	}
}

func updatesToJSON(updates []status.Update) []updateJSON {
	return httpx.ConvertAll(updates, func(update status.Update) updateJSON {
		return updateJSON{Status: string(update.Status), Message: update.Message, CreatedAt: update.CreatedAt.UTC()}
	})
}

// Le visiteur ne lit que des réponses réussies en JSON, sans mise en cache.
func (h *Handler) writeJSON(w http.ResponseWriter, status int, payload any) {
	if err := httpx.WriteJSONNoStore(w, status, payload); err != nil {
		h.logger.Warn("write json", "error", err)
	}
}
