package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
)

// Les alertes telles que le front les lit : des faits, jamais une phrase ;
// le navigateur fait la phrase avec les mêmes clés que le canal.

type alertJSON struct {
	ID             int64       `json:"id"`
	Kind           string      `json:"kind"`
	Severity       string      `json:"severity"`
	Status         string      `json:"status"`
	Muted          bool        `json:"muted"`
	Object         objectJSON  `json:"object"`
	MachineID      string      `json:"machine_id"`
	MachineName    string      `json:"machine_name"`
	ServiceID      string      `json:"service_id"`
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
	Threshold  int        `json:"threshold,omitempty"`
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
	// L'essai suivant d'une livraison en échec ; null : plus d'essai.
	NextAttemptAt *time.Time `json:"next_attempt_at"`
}

type channelJSON struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	URL    string `json:"url"`
	Format string `json:"format"`
	// Destination manque aux webhooks : leur URL dit tout.
	Destination   string `json:"destination,omitempty"`
	HasSecret     bool   `json:"has_secret"`
	MinSeverity   string `json:"min_severity"`
	NotifyResolve bool   `json:"notify_resolve"`
	ReceivesAudit bool   `json:"receives_audit"`
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
	Destination   string `json:"destination"`
	Secret        string `json:"secret"`
	ClearSecret   bool   `json:"clear_secret"`
	MinSeverity   string `json:"min_severity"`
	NotifyResolve bool   `json:"notify_resolve"`
	ReceivesAudit bool   `json:"receives_audit"`
	Enabled       bool   `json:"enabled"`
}

// Le résultat d'un test de canal : reçu, ou pourquoi pas.
type channelTestResponse struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason"`
	Code   int    `json:"code"`
}

// Une fenêtre pour taire ; l'interface dit « silence ». La cible vide tait
// tout.
type muteJSON struct {
	ID       int64      `json:"id"`
	Kind     string     `json:"kind"`
	Target   objectJSON `json:"target"`
	Reason   string     `json:"reason"`
	StartsAt time.Time  `json:"starts_at"`
	EndsAt   time.Time  `json:"ends_at"`
	Active   bool       `json:"active"`
	// Qui l'a posée, comme le journal d'audit le dit ; vide pour une
	// fenêtre recopiée d'un silence ou d'une maintenance.
	CreatedByKind string    `json:"created_by_kind"`
	CreatedByName string    `json:"created_by_name"`
	CreatedAt     time.Time `json:"created_at"`
	// Annoncé, le silence paraît en public comme une maintenance, sous ce
	// titre.
	Announce    bool   `json:"announce"`
	PublicTitle string `json:"public_title"`
}

type mutesResponse struct {
	Mutes []muteJSON `json:"mutes"`
}

// starts_at absent ou passé : tout de suite.
type muteRequest struct {
	Kind            string     `json:"kind"`
	TargetKind      string     `json:"target_kind"`
	TargetID        string     `json:"target_id"`
	Reason          string     `json:"reason"`
	StartsAt        *time.Time `json:"starts_at"`
	DurationMinutes int        `json:"duration_minutes"`
	Announce        bool       `json:"announce"`
	PublicTitle     string     `json:"public_title"`
}

func alertToJSON(found alert.Alert) alertJSON {
	return alertJSON{
		ID: found.ID, Kind: string(found.Kind), Severity: string(found.Severity), Status: string(found.Status), Muted: found.Muted,
		Object:    objectJSON{Kind: string(found.Object.Kind), ID: found.Object.ID, Name: found.Object.Name},
		MachineID: found.MachineID, MachineName: found.MachineName, ServiceID: found.ServiceID,
		Details: detailsJSON{
			Count: found.Details.Count, Percent: found.Details.Percent, MountPoint: found.Details.MountPoint, Threshold: found.Details.Threshold,
			ExitCode: found.Details.ExitCode, Reason: found.Details.Reason, Target: probe.RedactTarget(found.Details.Target),
			NotAfter: httpx.TimeOrNil(found.Details.NotAfter), Since: httpx.TimeOrNil(found.Details.Since),
		},
		OpenedAt: found.OpenedAt.UTC(), UpdatedAt: found.UpdatedAt.UTC(),
		ResolvedAt: httpx.TimeOrNil(found.ResolvedAt), AcknowledgedAt: httpx.TimeOrNil(found.AcknowledgedAt),
	}
}

func channelToJSON(channel alert.Channel) channelJSON {
	return channelJSON{
		ID: channel.ID, Name: channel.Name, URL: channel.URL, Format: string(channel.Format), Destination: channel.Destination, HasSecret: channel.HasSecret,
		MinSeverity: string(channel.MinSeverity), NotifyResolve: channel.NotifyResolve, ReceivesAudit: channel.ReceivesAudit, Enabled: channel.Enabled,
		Plain: channel.IsPlain(), CreatedAt: channel.CreatedAt.UTC(),
	}
}

func muteToJSON(mute alert.Mute, now time.Time) muteJSON {
	return muteJSON{
		ID: mute.ID, Kind: string(mute.Kind),
		Target: objectJSON{Kind: string(mute.Target.Kind), ID: mute.Target.ID, Name: mute.Target.Name},
		Reason: mute.Reason, StartsAt: mute.StartsAt.UTC(), EndsAt: mute.EndsAt.UTC(),
		Active: mute.IsActive(now), CreatedByKind: string(mute.CreatedBy.Kind), CreatedByName: mute.CreatedBy.Name, CreatedAt: mute.CreatedAt.UTC(),
		Announce: mute.Announce, PublicTitle: mute.PublicTitle,
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
	if s.failed(w, r, err, nil) {
		return
	}
	counts, err := s.reader.CountAlerts(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	response := alertsResponse{Alerts: httpx.ConvertAll(alerts, alertToJSON), Counts: alertCountsJSON{Open: counts.Open, Unacknowledged: counts.Unacknowledged}}
	s.writeAPI(w, http.StatusOK, response)
}

func (s *Server) getAlert(w http.ResponseWriter, r *http.Request) {
	id, ok := alertID(r)
	if !ok {
		s.apiNotFound(w)
		return
	}
	found, err := s.alerts.Get(r.Context(), id)
	if s.failed(w, r, err, alert.ErrNotFound) {
		return
	}
	deliveries, err := s.reader.ListDeliveries(r.Context(), id)
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, alertResponseOf(found, deliveries))
}

// alertResponseOf assemble la fiche d'une alerte ; l'API et l'outil MCP la
// rendent telle quelle.
func alertResponseOf(found alert.Alert, deliveries []alert.Delivery) alertResponse {
	response := alertResponse{Alert: alertToJSON(found), Deliveries: make([]deliveryJSON, 0, len(deliveries))}
	for _, delivery := range deliveries {
		response.Deliveries = append(response.Deliveries, deliveryJSON{
			ID: delivery.ID, ChannelID: delivery.ChannelID, Event: string(delivery.Event), Status: string(delivery.Status),
			Attempts: delivery.Attempts, Reason: string(delivery.Reason), Code: delivery.Code, UpdatedAt: delivery.UpdatedAt.UTC(),
			NextAttemptAt: httpx.TimeOrNil(delivery.NextAttemptAt),
		})
	}
	return response
}

func (s *Server) acknowledgeAlert(w http.ResponseWriter, r *http.Request) {
	id, ok := alertID(r)
	if !ok {
		s.apiNotFound(w)
		return
	}
	found, err := s.alerts.Acknowledge(r.Context(), id)
	if s.failed(w, r, err, alert.ErrNotFound) {
		return
	}
	s.writeAPI(w, http.StatusOK, alertToJSON(found))
}

type acknowledgeAlertsRequest struct {
	IDs []int64 `json:"ids"`
}

// Combien d'alertes de la sélection sont passées à acquittées ; les autres
// l'étaient déjà, ou se sont résolues entre-temps.
type acknowledgeAlertsResponse struct {
	Acknowledged int `json:"acknowledged"`
}

func (s *Server) acknowledgeAlerts(w http.ResponseWriter, r *http.Request) {
	var request acknowledgeAlertsRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	acknowledged, err := s.alerts.AcknowledgeMany(r.Context(), request.IDs)
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, acknowledgeAlertsResponse{Acknowledged: acknowledged})
}

// --- Canaux ---

func (s *Server) listChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := s.reader.ListChannels(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	response := channelsResponse{Channels: httpx.ConvertAll(channels, channelToJSON)}
	s.writeAPI(w, http.StatusOK, response)
}

func (s *Server) getChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := alertID(r)
	if !ok {
		s.apiNotFound(w)
		return
	}
	channel, err := s.reader.GetChannel(r.Context(), id)
	if s.failed(w, r, err, alert.ErrNotFound) {
		return
	}
	s.writeAPI(w, http.StatusOK, channelToJSON(channel))
}

func channelDefinition(request channelRequest) alert.ChannelDefinition {
	return alert.ChannelDefinition{
		Name: request.Name, URL: request.URL, Format: alert.Format(request.Format), Destination: request.Destination, Secret: request.Secret, ClearSecret: request.ClearSecret,
		MinSeverity: alert.Severity(request.MinSeverity), NotifyResolve: request.NotifyResolve, ReceivesAudit: request.ReceivesAudit, Enabled: request.Enabled,
	}
}

func (s *Server) createChannel(w http.ResponseWriter, r *http.Request) {
	var request channelRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	channel, err := s.alerts.CreateChannel(r.Context(), channelDefinition(request))
	if s.failed(w, r, err, alert.ErrNotFound) {
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
	if s.failed(w, r, err, alert.ErrNotFound) {
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
	channel, err := s.reader.GetChannel(r.Context(), id)
	if s.failed(w, r, err, alert.ErrNotFound) {
		return
	}
	outcome, err := s.notifier.Test(r.Context(), channel)
	if s.failed(w, r, err, nil) {
		return
	}
	s.writeAPI(w, http.StatusOK, channelTestResponse{OK: outcome.OK(), Reason: string(outcome.Reason), Code: outcome.Code})
}

// --- Fenêtres pour taire ---

func (s *Server) listMutes(w http.ResponseWriter, r *http.Request) {
	mutes, err := s.reader.ListMutes(r.Context())
	if s.failed(w, r, err, nil) {
		return
	}
	now := s.now()
	response := mutesResponse{Mutes: httpx.ConvertAll(mutes, func(mute alert.Mute) muteJSON { return muteToJSON(mute, now) })}
	s.writeAPI(w, http.StatusOK, response)
}

func (s *Server) createMute(w http.ResponseWriter, r *http.Request) {
	var request muteRequest
	if !s.readAPI(w, r, &request) {
		return
	}
	mute, err := s.alerts.CreateMute(r.Context(), alert.MuteDefinition{
		Kind:     alert.Kind(request.Kind),
		Target:   alert.Target{Kind: alert.TargetKind(request.TargetKind), ID: request.TargetID},
		Reason:   request.Reason,
		StartsAt: timeOrZero(request.StartsAt),
		Duration: time.Duration(request.DurationMinutes) * time.Minute,
		Announce: request.Announce, PublicTitle: request.PublicTitle,
	})
	if s.failed(w, r, err, alert.ErrNotFound) {
		return
	}
	s.writeAPI(w, http.StatusCreated, muteToJSON(mute, s.now()))
}

func (s *Server) deleteMute(w http.ResponseWriter, r *http.Request) {
	id, ok := alertID(r)
	if !ok {
		s.apiNotFound(w)
		return
	}
	s.finishAPIAction(w, r, s.alerts.DeleteMute(r.Context(), id), alert.ErrNotFound)
}
