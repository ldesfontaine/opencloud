package server

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
)

const (
	// Une page du journal : ce que l'écran montre d'un coup ; au-delà, la
	// page suivante, par next_before.
	auditDefaultLimit = 50
	auditMaxLimit     = 200
	codeBadQuery      = "bad_query"
)

type auditEventJSON struct {
	ID         int64             `json:"id"`
	At         time.Time         `json:"at"`
	ActorKind  string            `json:"actor_kind"`
	ActorName  string            `json:"actor_name"`
	Action     string            `json:"action"`
	ObjectKind string            `json:"object_kind"`
	ObjectID   string            `json:"object_id"`
	ObjectName string            `json:"object_name"`
	Details    map[string]string `json:"details"`
	ApplyID    *string           `json:"apply_id"`
}

type auditResponse struct {
	Events []auditEventJSON `json:"events"`
	// NextBefore est le curseur de la page suivante ; null quand la page
	// n'était pas pleine.
	NextBefore *int64 `json:"next_before"`
}

// listAudit rend une page du journal d'audit, du plus récent au plus ancien,
// filtrée par objet au besoin.
func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	query, ok := auditQueryOf(r.URL.Query())
	if !ok {
		s.writeAPIError(w, http.StatusBadRequest, codeBadQuery)
		return
	}
	response, err := s.auditPage(r.Context(), query)
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, response)
}

// auditQueryOf lit les paramètres ; un nombre illisible ou une sorte
// d'objet inconnue est un refus, pas un filtre ignoré.
func auditQueryOf(values url.Values) (audit.Query, bool) {
	query := audit.Query{
		ObjectKind: audit.ObjectKind(values.Get("object_kind")),
		ObjectID:   values.Get("object_id"),
	}
	if text := values.Get("limit"); text != "" {
		limit, err := strconv.Atoi(text)
		if err != nil || limit < 1 {
			return audit.Query{}, false
		}
		query.Limit = limit
	}
	if text := values.Get("before"); text != "" {
		before, err := strconv.ParseInt(text, 10, 64)
		if err != nil || before < 1 {
			return audit.Query{}, false
		}
		query.Before = before
	}
	return query, isKnownObjectKind(query.ObjectKind)
}

func isKnownObjectKind(kind audit.ObjectKind) bool {
	return kind == "" || slices.Contains(audit.ObjectKinds(), kind)
}

// auditPage borne la page et l'assemble : l'API et l'outil MCP la rendent
// telle quelle.
func (s *Server) auditPage(ctx context.Context, query audit.Query) (auditResponse, error) {
	if query.Limit <= 0 {
		query.Limit = auditDefaultLimit
	}
	query.Limit = min(query.Limit, auditMaxLimit)
	events, err := s.reader.ListAuditEvents(ctx, query)
	if err != nil {
		return auditResponse{}, err
	}
	response := auditResponse{Events: httpx.ConvertAll(events, auditEventToJSON)}
	if len(events) == query.Limit {
		last := events[len(events)-1].ID
		response.NextBefore = &last
	}
	return response, nil
}

func auditEventToJSON(event audit.Event) auditEventJSON {
	converted := auditEventJSON{
		ID: event.ID, At: event.At.UTC(),
		ActorKind: string(event.Actor.Kind), ActorName: event.Actor.Name,
		Action:     string(event.Action),
		ObjectKind: string(event.ObjectKind), ObjectID: event.ObjectID, ObjectName: event.ObjectName,
		Details: event.Details,
	}
	if converted.Details == nil {
		converted.Details = map[string]string{}
	}
	if event.ApplyID != "" {
		applyID := event.ApplyID
		converted.ApplyID = &applyID
	}
	return converted
}
