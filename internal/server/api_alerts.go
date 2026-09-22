package server

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
)

// Les alertes telles que le front les lit : des faits, jamais une phrase ;
// le navigateur fait la phrase avec les mêmes clés que le canal.

type alertJSON struct {
	ID             int64       `json:"id"`
	Kind           string      `json:"kind"`
	Severity       string      `json:"severity"`
	Status         string      `json:"status"`
	Silenced       bool        `json:"silenced"`
	Object         objectJSON  `json:"object"`
	MachineID      string      `json:"machine_id"`
	MachineName    string      `json:"machine_name"`
	Details        detailsJSON `json:"details"`
	OpenedAt       time.Time   `json:"opened_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
	ResolvedAt     *time.Time  `json:"resolved_at"`
	AcknowledgedAt *time.Time  `json:"acknowledged_at"`
}

type objectJSON struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

type detailsJSON struct {
	Count      int        `json:"count,omitempty"`
	Percent    int        `json:"percent,omitempty"`
	MountPoint string     `json:"mount_point,omitempty"`
	ExitCode   int        `json:"exit_code,omitempty"`
	Reason     string     `json:"reason,omitempty"`
	Target     string     `json:"target,omitempty"`
	NotAfter   *time.Time `json:"not_after,omitempty"`
	Since      *time.Time `json:"since,omitempty"`
}

type alertCountsJSON struct {
	Open           int `json:"open"`
	Unacknowledged int `json:"unacknowledged"`
}

type alertsResponse struct {
	Alerts []alertJSON     `json:"alerts"`
	Counts alertCountsJSON `json:"counts"`
}

type alertResponse struct {
	Alert      alertJSON      `json:"alert"`
	Deliveries []deliveryJSON `json:"deliveries"`
}

type deliveryJSON struct {
	ID        int64     `json:"id"`
	ChannelID int64     `json:"channel_id"`
	Event     string    `json:"event"`
	Status    string    `json:"status"`
	Attempts  int       `json:"attempts"`
	Reason    string    `json:"reason"`
	Code      int       `json:"code"`
	UpdatedAt time.Time `json:"updated_at"`
}

type channelJSON struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	URL           string `json:"url"`
	Format        string `json:"format"`
	HasSecret     bool   `json:"has_secret"`
	MinSeverity   string `json:"min_severity"`
	NotifyResolve bool   `json:"notify_resolve"`
	Enabled       bool   `json:"enabled"`
	// Plain : l'URL est en http, le canal parle en clair ; l'interface l'écrit.
	Plain     bool      `json:"plain"`
	CreatedAt time.Time `json:"created_at"`
}

type channelsResponse struct {
	Channels []channelJSON `json:"channels"`
}

type channelRequest struct {
	Name          string `json:"name"`
	URL           string `json:"url"`
	Format        string `json:"format"`
	Secret        string `json:"secret"`
	ClearSecret   bool   `json:"clear_secret"`
	MinSeverity   string `json:"min_severity"`
	NotifyResolve bool   `json:"notify_resolve"`
	Enabled       bool   `json:"enabled"`
}

// Le résultat d'un test de canal : reçu, ou pourquoi pas.
type channelTestResponse struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason"`
	Code   int    `json:"code"`
}

type silenceJSON struct {
	ID        int64      `json:"id"`
	Kind      string     `json:"kind"`
	Object    objectJSON `json:"object"`
	Reason    string     `json:"reason"`
	StartsAt  time.Time  `json:"starts_at"`
	EndsAt    time.Time  `json:"ends_at"`
	Active    bool       `json:"active"`
	CreatedAt time.Time  `json:"created_at"`
}

type silencesResponse struct {
	Silences []silenceJSON `json:"silences"`
}

type silenceRequest struct {
	Kind            string `json:"kind"`
	ObjectKind      string `json:"object_kind"`
	ObjectID        string `json:"object_id"`
	ObjectName      string `json:"object_name"`
	Reason          string `json:"reason"`
	DurationMinutes int    `json:"duration_minutes"`
}

func alertToJSON(found alert.Alert) alertJSON {
	return alertJSON{
		ID: found.ID, Kind: string(found.Kind), Severity: string(found.Severity), Status: string(found.Status), Silenced: found.Silenced,
		Object:    objectJSON{Kind: string(found.Object.Kind), ID: found.Object.ID, Name: found.Object.Name},
		MachineID: found.MachineID, MachineName: found.MachineName,
		Details: detailsJSON{
			Count: found.Details.Count, Percent: found.Details.Percent, MountPoint: found.Details.MountPoint,
			ExitCode: found.Details.ExitCode, Reason: found.Details.Reason, Target: found.Details.Target,
			NotAfter: timeOrNil(found.Details.NotAfter), Since: timeOrNil(found.Details.Since),
		},
		OpenedAt: found.OpenedAt.UTC(), UpdatedAt: found.UpdatedAt.UTC(),
		ResolvedAt: timeOrNil(found.ResolvedAt), AcknowledgedAt: timeOrNil(found.AcknowledgedAt),
	}
}

func channelToJSON(channel alert.Channel) channelJSON {
	return channelJSON{
		ID: channel.ID, Name: channel.Name, URL: channel.URL, Format: string(channel.Format), HasSecret: channel.HasSecret,
		MinSeverity: string(channel.MinSeverity), NotifyResolve: channel.NotifyResolve, Enabled: channel.Enabled,
		Plain: channel.IsPlain(), CreatedAt: channel.CreatedAt.UTC(),
	}
}

func silenceToJSON(silence alert.Silence, now time.Time) silenceJSON {
	return silenceJSON{
		ID: silence.ID, Kind: string(silence.Kind),
		Object: objectJSON{Kind: string(silence.Object.Kind), ID: silence.Object.ID, Name: silence.Object.Name},
		Reason: silence.Reason, StartsAt: silence.StartsAt.UTC(), EndsAt: silence.EndsAt.UTC(),
		Active: silence.IsActive(now), CreatedAt: silence.CreatedAt.UTC(),
	}
}

func alertID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

// --- Alertes ---

func (s *Server) listAlerts(w http.ResponseWriter, r *http.Request) {
	status := alert.StatusOpen
	if r.URL.Query().Get("status") == string(alert.StatusResolved) {
		status = alert.StatusResolved
	}
	alerts, err := s.alerts.List(r.Context(), status)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	counts, err := s.alerts.Count(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	response := alertsResponse{Alerts: make([]alertJSON, 0, len(alerts)), Counts: alertCountsJSON{Open: counts.Open, Unacknowledged: counts.Unacknowledged}}
	for _, found := range alerts {
		response.Alerts = append(response.Alerts, alertToJSON(found))
	}
	s.writeAPI(w, http.StatusOK, response)
}

func (s *Server) getAlert(w http.ResponseWriter, r *http.Request) {
	id, ok := alertID(r)
	if !ok {
		s.apiNotFound(w)
		return
	}
	found, err := s.alerts.Get(r.Context(), id)
	if errors.Is(err, alert.ErrNotFound) {
		s.apiNotFound(w)
		return
	}
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	deliveries, err := s.alerts.Deliveries(r.Context(), id)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	response := alertResponse{Alert: alertToJSON(found), Deliveries: make([]deliveryJSON, 0, len(deliveries))}
	for _, delivery := range deliveries {
		response.Deliveries = append(response.Deliveries, deliveryJSON{
			ID: delivery.ID, ChannelID: delivery.ChannelID, Event: string(delivery.Event), Status: string(delivery.Status),
			Attempts: delivery.Attempts, Reason: string(delivery.Reason), Code: delivery.Code, UpdatedAt: delivery.UpdatedAt.UTC(),
		})
	}
	s.writeAPI(w, http.StatusOK, response)
}

func (s *Server) acknowledgeAlert(w http.ResponseWriter, r *http.Request) {
	id, ok := alertID(r)
	if !ok {
		s.apiNotFound(w)
		return
	}
	found, err := s.alerts.Acknowledge(r.Context(), id)
	switch {
	case errors.Is(err, alert.ErrNotFound):
		s.apiNotFound(w)
	case errors.Is(err, alert.ErrAlreadyClosed):
		s.apiRefuse(w, http.StatusConflict, "alert.already_resolved")
	case err != nil:
		s.apiInternalError(w, r, err)
	default:
		s.writeAPI(w, http.StatusOK, alertToJSON(found))
	}
}

// --- Canaux ---

func (s *Server) listChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := s.alerts.Channels(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	response := channelsResponse{Channels: make([]channelJSON, 0, len(channels))}
	for _, channel := range channels {
		response.Channels = append(response.Channels, channelToJSON(channel))
	}
	s.writeAPI(w, http.StatusOK, response)
}

func (s *Server) getChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := alertID(r)
	if !ok {
		s.apiNotFound(w)
		return
	}
	channel, err := s.alerts.GetChannel(r.Context(), id)
	if errors.Is(err, alert.ErrNotFound) {
		s.apiNotFound(w)
		return
	}
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusOK, channelToJSON(channel))
}

func channelDefinition(request channelRequest) alert.ChannelDefinition {
	return alert.ChannelDefinition{
		Name: request.Name, URL: request.URL, Format: alert.Format(request.Format), Secret: request.Secret, ClearSecret: request.ClearSecret,
		MinSeverity: alert.Severity(request.MinSeverity), NotifyResolve: request.NotifyResolve, Enabled: request.Enabled,
	}
}

func (s *Server) createChannel(w http.ResponseWriter, r *http.Request) {
	var request channelRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	channel, err := s.alerts.CreateChannel(r.Context(), channelDefinition(request))
	if s.refuseAlert(w, r, err) {
		return
	}
	s.writeAPI(w, http.StatusCreated, channelToJSON(channel))
}

func (s *Server) updateChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := alertID(r)
	if !ok {
		s.apiNotFound(w)
		return
	}
	var request channelRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	channel, err := s.alerts.UpdateChannel(r.Context(), id, channelDefinition(request))
	if s.refuseAlert(w, r, err) {
		return
	}
	s.writeAPI(w, http.StatusOK, channelToJSON(channel))
}

func (s *Server) deleteChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := alertID(r)
	if !ok {
		s.apiNotFound(w)
		return
	}
	s.finishAPIAction(w, r, s.alerts.DeleteChannel(r.Context(), id), alert.ErrNotFound)
}

// testChannel envoie un corps de test, tout de suite, et répond ce que le
// récepteur a dit.
func (s *Server) testChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := alertID(r)
	if !ok {
		s.apiNotFound(w)
		return
	}
	channel, err := s.alerts.GetChannel(r.Context(), id)
	if errors.Is(err, alert.ErrNotFound) {
		s.apiNotFound(w)
		return
	}
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	outcome, err := s.notifier.Test(r.Context(), channel)
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	s.writeAPI(w, http.StatusOK, channelTestResponse{OK: outcome.OK(), Reason: string(outcome.Reason), Code: outcome.Code})
}

// --- Silences ---

func (s *Server) listSilences(w http.ResponseWriter, r *http.Request) {
	silences, err := s.alerts.Silences(r.Context())
	if err != nil {
		s.apiInternalError(w, r, err)
		return
	}
	now := s.now()
	response := silencesResponse{Silences: make([]silenceJSON, 0, len(silences))}
	for _, silence := range silences {
		response.Silences = append(response.Silences, silenceToJSON(silence, now))
	}
	s.writeAPI(w, http.StatusOK, response)
}

func (s *Server) createSilence(w http.ResponseWriter, r *http.Request) {
	var request silenceRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	silence, err := s.alerts.CreateSilence(r.Context(), alert.SilenceDefinition{
		Kind:     alert.Kind(request.Kind),
		Object:   alert.Object{Kind: alert.ObjectKind(request.ObjectKind), ID: request.ObjectID, Name: request.ObjectName},
		Reason:   request.Reason,
		Duration: time.Duration(request.DurationMinutes) * time.Minute,
	})
	if s.refuseAlert(w, r, err) {
		return
	}
	s.writeAPI(w, http.StatusCreated, silenceToJSON(silence, s.now()))
}

func (s *Server) deleteSilence(w http.ResponseWriter, r *http.Request) {
	id, ok := alertID(r)
	if !ok {
		s.apiNotFound(w)
		return
	}
	s.finishAPIAction(w, r, s.alerts.DeleteSilence(r.Context(), id), alert.ErrNotFound)
}

// refuseAlert répond au refus d'un canal ou d'un silence par la clé du
// catalogue ; vrai quand la requête est finie.
func (s *Server) refuseAlert(w http.ResponseWriter, r *http.Request, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, alert.ErrNotFound) {
		s.apiNotFound(w)
		return true
	}
	if key, refused := alertRefusalKey(err); refused {
		s.apiRefuse(w, http.StatusUnprocessableEntity, key)
		return true
	}
	s.apiInternalError(w, r, err)
	return true
}

func alertRefusalKey(err error) (string, bool) {
	switch {
	case errors.Is(err, alert.ErrChannelNameInvalid):
		return "channel.name_invalid", true
	case errors.Is(err, alert.ErrChannelURLInvalid):
		return "channel.url_invalid", true
	case errors.Is(err, alert.ErrChannelURLForbidden):
		return "channel.url_forbidden", true
	case errors.Is(err, alert.ErrChannelFormatInvalid):
		return "channel.format_invalid", true
	case errors.Is(err, alert.ErrChannelSecretInvalid):
		return "channel.secret_invalid", true
	case errors.Is(err, alert.ErrChannelSeverityInvalid):
		return "channel.severity_invalid", true
	case errors.Is(err, alert.ErrTooManyChannels):
		return "channel.too_many", true
	case errors.Is(err, alert.ErrSilenceInvalid):
		return "silence.invalid", true
	case errors.Is(err, alert.ErrSilenceDurationInvalid):
		return "silence.duration_invalid", true
	}
	return "", false
}
