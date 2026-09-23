package server

import (
	"context"
	"errors"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/service"
	"github.com/ldesfontaine/opencloud/internal/status"
	"github.com/ldesfontaine/opencloud/internal/update"
)

// Les sept écritures : ce que l'interface fait déjà, et qui ne touche
// jamais une machine. Chacune rend l'objet tel que l'API le relit, et
// refuse avec la même clé que l'API.
func (s *Server) registerWriteTools(server *gomcp.Server, readOnly bool) {
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "acknowledge_alert",
		Description: "Acknowledge an open alert: it stays open but leaves the unacknowledged counter. Fails with alert.already_resolved on a resolved alert; acknowledging twice is harmless.",
		Annotations: writeHints(true),
	}, guarded(readOnly, s.acknowledgeAlertTool))
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "pause_probe",
		Description: "Pause a probe: its machine stops checking the target and its status becomes paused. Pausing twice is harmless.",
		Annotations: writeHints(true),
	}, guarded(readOnly, s.pauseProbeTool))
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "resume_probe",
		Description: "Resume a paused probe: it restarts as new and is checked right away. Fails with probe.not_paused when it is not paused.",
		Annotations: writeHints(true),
	}, guarded(readOnly, s.resumeProbeTool))
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "pause_job",
		Description: "Pause a job: no more deadline, so it can no longer be late; an open run is closed as timed out. Pausing twice is harmless.",
		Annotations: writeHints(true),
	}, guarded(readOnly, s.pauseJobTool))
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "resume_job",
		Description: "Resume a paused job: a full interval starts now. Fails with job.not_paused when it is not paused.",
		Annotations: writeHints(true),
	}, guarded(readOnly, s.resumeJobTool))
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "open_incident",
		Description: "Open an incident on the PUBLIC status page: visitors read the title and message immediately. impact is degraded, down or maintenance; status is investigating, identified or monitoring for an outage, scheduled or in_progress for a maintenance, which also needs starts_at and ends_at (RFC 3339). At least one component_id from get_status_page.",
		Annotations: writeHints(false),
	}, guarded(readOnly, s.openIncidentTool))
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "add_incident_update",
		Description: "Post an update to the PUBLIC thread of an open incident and move it to that status; status=resolved closes it. Fails with incident.already_resolved on a resolved incident.",
		Annotations: writeHints(false),
	}, guarded(readOnly, s.addIncidentUpdateTool))
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "create_silence",
		Description: "Silence alerts for a while: a kind of alert (from list_alerts), an object (object_kind and object_id), or a kind on an object; never everything. Silenced alerts still open and show, they are not delivered to channels.",
		Annotations: writeHints(false),
	}, guarded(readOnly, s.createSilenceTool))
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "set_update_policy",
		Description: "Set what the operator wants about a service's image updates: '' follows updates, 'pinned' hides newer tags and digests, 'excluded' never queries the registry. Never pulls or restarts anything.",
		Annotations: writeHints(true),
	}, guarded(readOnly, s.setUpdatePolicyTool))
}

// guarded ferme une écriture en lecture seule, avec la clé que le modèle
// lit ; sinon elle passe telle quelle.
func guarded[In any](readOnly bool, handler func(context.Context, In) (*gomcp.CallToolResult, any, error)) gomcp.ToolHandlerFor[In, any] {
	return func(ctx context.Context, _ *gomcp.CallToolRequest, input In) (*gomcp.CallToolResult, any, error) {
		if readOnly {
			return toolRefuse(codeReadOnly)
		}
		return handler(ctx, input)
	}
}

// --- Entrées ---

type openIncidentInput struct {
	Title        string   `json:"title" jsonschema:"Public title, 1 to 120 characters"`
	Impact       string   `json:"impact" jsonschema:"degraded, down or maintenance"`
	Status       string   `json:"status" jsonschema:"investigating, identified or monitoring for an outage; scheduled or in_progress for a maintenance"`
	Message      string   `json:"message,omitempty" jsonschema:"First public update, up to 2000 characters"`
	ComponentIDs []string `json:"component_ids" jsonschema:"Affected components, at least one"`
	StartsAt     string   `json:"starts_at,omitempty" jsonschema:"Maintenance window start, RFC 3339"`
	EndsAt       string   `json:"ends_at,omitempty" jsonschema:"Maintenance window end, RFC 3339"`
}

type incidentUpdateInput struct {
	IncidentID string `json:"incident_id" jsonschema:"Incident id, as returned by list_incidents"`
	Status     string `json:"status" jsonschema:"New status: investigating, identified, monitoring, in_progress or resolved"`
	Message    string `json:"message" jsonschema:"Public update text, 1 to 2000 characters"`
}

type silenceInput struct {
	Kind            string `json:"kind,omitempty" jsonschema:"Alert kind to silence, or empty for every kind on the object"`
	ObjectKind      string `json:"object_kind,omitempty" jsonschema:"machine, service, heartbeat, probe or volume; empty to silence a kind everywhere"`
	ObjectID        string `json:"object_id,omitempty" jsonschema:"The object's id"`
	ObjectName      string `json:"object_name,omitempty" jsonschema:"The object's name, kept for display"`
	Reason          string `json:"reason,omitempty" jsonschema:"Why, for the operator"`
	DurationMinutes int    `json:"duration_minutes" jsonschema:"How long, in minutes"`
}

type updatePolicyInput struct {
	ServiceID string `json:"service_id" jsonschema:"Service id, as returned by list_services"`
	Policy    string `json:"policy" jsonschema:"'' to follow, 'pinned' or 'excluded'"`
}

// --- Handlers ---

func (s *Server) acknowledgeAlertTool(ctx context.Context, input alertInput) (*gomcp.CallToolResult, any, error) {
	found, err := s.alerts.Acknowledge(ctx, input.AlertID)
	switch {
	case errors.Is(err, alert.ErrNotFound):
		return toolRefuse(codeNotFound)
	case errors.Is(err, alert.ErrAlreadyClosed):
		return toolRefuse("alert.already_resolved")
	case err != nil:
		return s.toolInternal("acknowledge_alert", err)
	}
	return toolJSON(alertToJSON(found))
}

func (s *Server) pauseProbeTool(ctx context.Context, input probeInput) (*gomcp.CallToolResult, any, error) {
	err := s.probes.Pause(ctx, input.ProbeID)
	if errors.Is(err, probe.ErrNotFound) {
		return toolRefuse(codeNotFound)
	}
	if err != nil {
		return s.toolInternal("pause_probe", err)
	}
	return s.probeAfterWrite(ctx, "pause_probe", input.ProbeID)
}

func (s *Server) resumeProbeTool(ctx context.Context, input probeInput) (*gomcp.CallToolResult, any, error) {
	err := s.probes.Resume(ctx, input.ProbeID)
	switch {
	case errors.Is(err, probe.ErrNotFound):
		return toolRefuse(codeNotFound)
	case errors.Is(err, probe.ErrNotPaused):
		return toolRefuse("probe.not_paused")
	case err != nil:
		return s.toolInternal("resume_probe", err)
	}
	return s.probeAfterWrite(ctx, "resume_probe", input.ProbeID)
}

func (s *Server) probeAfterWrite(ctx context.Context, tool, id string) (*gomcp.CallToolResult, any, error) {
	found, err := s.probes.Get(ctx, id)
	if err != nil {
		return s.toolInternal(tool, err)
	}
	online, err := s.onlineMachines(ctx)
	if err != nil {
		return s.toolInternal(tool, err)
	}
	return toolJSON(probeToJSON(found, online[found.MachineID]))
}

func (s *Server) pauseJobTool(ctx context.Context, input jobInput) (*gomcp.CallToolResult, any, error) {
	err := s.heartbeats.Pause(ctx, input.JobID)
	if errors.Is(err, heartbeat.ErrNotFound) {
		return toolRefuse(codeNotFound)
	}
	if err != nil {
		return s.toolInternal("pause_job", err)
	}
	return s.jobAfterWrite(ctx, "pause_job", input.JobID)
}

func (s *Server) resumeJobTool(ctx context.Context, input jobInput) (*gomcp.CallToolResult, any, error) {
	err := s.heartbeats.Resume(ctx, input.JobID)
	switch {
	case errors.Is(err, heartbeat.ErrNotFound):
		return toolRefuse(codeNotFound)
	case errors.Is(err, heartbeat.ErrNotPaused):
		return toolRefuse("job.not_paused")
	case err != nil:
		return s.toolInternal("resume_job", err)
	}
	return s.jobAfterWrite(ctx, "resume_job", input.JobID)
}

func (s *Server) jobAfterWrite(ctx context.Context, tool, id string) (*gomcp.CallToolResult, any, error) {
	found, err := s.heartbeats.Get(ctx, id)
	if err != nil {
		return s.toolInternal(tool, err)
	}
	return toolJSON(jobToJSON(found))
}

func (s *Server) openIncidentTool(ctx context.Context, input openIncidentInput) (*gomcp.CallToolResult, any, error) {
	startsAt, ok := parseInstant(input.StartsAt)
	if !ok {
		return toolRefuse("incident.window_invalid")
	}
	endsAt, ok := parseInstant(input.EndsAt)
	if !ok {
		return toolRefuse("incident.window_invalid")
	}
	opened, err := s.status.OpenIncident(ctx, status.IncidentDefinition{
		Title: input.Title, Impact: status.Impact(input.Impact), Status: status.IncidentStatus(input.Status),
		Message: input.Message, ComponentIDs: input.ComponentIDs, StartsAt: startsAt, EndsAt: endsAt,
	})
	if key, refused := statusRefusalKey(err); refused {
		return toolRefuse(key)
	}
	if err != nil {
		return s.toolInternal("open_incident", err)
	}
	return toolJSON(incidentToJSON(opened))
}

func (s *Server) addIncidentUpdateTool(ctx context.Context, input incidentUpdateInput) (*gomcp.CallToolResult, any, error) {
	updated, err := s.status.AddUpdate(ctx, input.IncidentID, status.IncidentStatus(input.Status), input.Message)
	switch {
	case errors.Is(err, status.ErrNotFound):
		return toolRefuse(codeNotFound)
	case errors.Is(err, status.ErrResolved):
		return toolRefuse("incident.already_resolved")
	}
	if key, refused := statusRefusalKey(err); refused {
		return toolRefuse(key)
	}
	if err != nil {
		return s.toolInternal("add_incident_update", err)
	}
	return toolJSON(incidentToJSON(updated))
}

func (s *Server) createSilenceTool(ctx context.Context, input silenceInput) (*gomcp.CallToolResult, any, error) {
	silence, err := s.alerts.CreateSilence(ctx, alert.SilenceDefinition{
		Kind:     alert.Kind(input.Kind),
		Object:   alert.Object{Kind: alert.ObjectKind(input.ObjectKind), ID: input.ObjectID, Name: input.ObjectName},
		Reason:   input.Reason,
		Duration: time.Duration(input.DurationMinutes) * time.Minute,
	})
	if key, refused := alertRefusalKey(err); refused {
		return toolRefuse(key)
	}
	if err != nil {
		return s.toolInternal("create_silence", err)
	}
	return toolJSON(silenceToJSON(silence, s.now()))
}

func (s *Server) setUpdatePolicyTool(ctx context.Context, input updatePolicyInput) (*gomcp.CallToolResult, any, error) {
	err := s.updates.SetPolicy(ctx, input.ServiceID, service.UpdatePolicy(input.Policy))
	switch {
	case errors.Is(err, update.ErrBadPolicy):
		return toolRefuse("update.policy_invalid")
	case errors.Is(err, service.ErrNotFound):
		return toolRefuse(codeNotFound)
	case err != nil:
		return s.toolInternal("set_update_policy", err)
	}
	item, err := s.services.Get(ctx, input.ServiceID)
	if err != nil {
		return s.toolInternal("set_update_policy", err)
	}
	response, err := s.serviceResponseOf(ctx, item)
	if err != nil {
		return s.toolInternal("set_update_policy", err)
	}
	return toolJSON(response.Service)
}

// parseInstant lit un instant RFC 3339 facultatif ; vide vaut zéro.
func parseInstant(value string) (time.Time, bool) {
	if value == "" {
		return time.Time{}, true
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}
