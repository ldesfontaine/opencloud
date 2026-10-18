package server

import (
	"context"
	"errors"
	"net/url"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/service"
	"github.com/ldesfontaine/opencloud/internal/status"
)

// Les dix-sept lectures : chacune rend les faits de l'API, sous le même JSON.
// Aucun secret d'openCloud n'en sort : ni l'URL de ping d'une tâche, ni les
// jetons en attente d'une machine, ni l'URL complète d'un canal. Les
// journaux d'un conteneur, eux, sont bruts et peuvent en contenir un.
func (s *Server) registerReadTools(server *gomcp.Server) {
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "get_overview",
		Description: brand.ProductName + " version and the counters of the overview: machines (total, online), jobs and probes (total, needing attention), services (total, needing attention, with a newer image), certificates (total, expiring, expired, soonest expiry), open incidents, alerts (open, unacknowledged).",
		Annotations: readOnlyHints(),
	}, s.getOverviewTool)
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "list_machines",
		Description: "List every machine " + brand.ProductName + " manages: id, name, kind (local is the " + brand.ProductName + " host itself), hostname, address, OS, architecture, agent version, whether its agent is connected (online) and when it was last seen. Over stdio (" + brand.Command + " mcp), online means a signal within the last 2 minutes.",
		Annotations: readOnlyHints(),
	}, s.listMachinesTool)
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "get_machine",
		Description: "One machine with its latest resource reading (CPU, load, memory, swap, disks per mount point, network rates) when it is fresh enough, and what it reports about its Docker engine.",
		Annotations: readOnlyHints(),
	}, s.getMachineTool)
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "get_machine_network",
		Description: "The Docker network topology of one machine: its services with their exposure findings, the networks grouping them, and the edges from the Internet to published ports and between dependent services.",
		Annotations: readOnlyHints(),
	}, s.getMachineNetworkTool)
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "list_services",
		Description: "List Docker containers (services) across machines, optionally filtered by machine_id or Compose project (group), paged. Each service carries its state, exit code, health, restart count, image, published ports, exposure findings, the latest image check (a newer tag or digest) and its current CPU/memory sample. Derive 'failing' from state=exited with exit_code!=0 or health=unhealthy.",
		Annotations: readOnlyHints(),
	}, s.listServicesTool)
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "get_service",
		Description: "One service with the Docker engine of its machine and its last 50 state transitions (start, die, health changes, with the raw log snippet captured on an abnormal exit, which can contain secrets).",
		Annotations: readOnlyHints(),
	}, s.getServiceTool)
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "list_declared_services",
		Description: "Declared services of the directory with their gap against what Docker shows (conform, absent, diverged, unverifiable on latest or untagged, unobserved for binaries, weak name match), and the containers present but undeclared, grouped by Compose project or name, flagged when ignored.",
		Annotations: readOnlyHints(),
	}, s.listDeclaredServicesTool)
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "get_service_logs",
		Description: "The last lines of a container's stdout/stderr, fetched live from the machine's agent (tail 1 to 500, default 100). Fails with service.machine_offline when the agent is not connected. Raw container output, not filtered: it can contain secrets and is sent to whoever runs the model. Over stdio (" + brand.Command + " mcp) the agents' streams are not held: it always fails with service.logs_unavailable_locally, use the HTTP transport.",
		Annotations: readOnlyHints(),
	}, s.getServiceLogsTool)
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "list_jobs",
		Description: "List scheduled jobs watched by heartbeat pings: status (new, up, running, late, failed, paused), interval and grace in seconds, last ping, next deadline, last exit code and duration. The ping URL is never returned.",
		Annotations: readOnlyHints(),
	}, s.listJobsTool)
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "get_job",
		Description: "One job with its last 20 runs (start, end, duration, exit code, outcome, payload) and last 20 pings (kind, source address, method). The ping URL is never returned.",
		Annotations: readOnlyHints(),
	}, s.getJobTool)
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "list_probes",
		Description: "List HTTP and TCP probes, optionally filtered by machine_id, paged: status (new, up, degraded, down, paused), target, machine that probes and whether it is online, interval, thresholds, last check with its reason, the TLS certificate last seen (subject, issuer, validity dates, fingerprint, chain_valid, hostname_match, OCSP), and per-probe daily counts over 30 days to compute uptime.",
		Annotations: readOnlyHints(),
	}, s.listProbesTool)
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "get_probe",
		Description: "One probe with its uptime windows (total and successful checks per window), 90 days of daily counts, and its last 50 checks (outcome, duration, HTTP code, reason).",
		Annotations: readOnlyHints(),
	}, s.getProbeTool)
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "get_status_page",
		Description: "The public status page as the operator sees it: the global state, each component with its members (machine, service, job or probe), its derived state and the effective state after incidents, plus the open incidents.",
		Annotations: readOnlyHints(),
	}, s.getStatusPageTool)
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "list_incidents",
		Description: "List status page incidents, open by default or resolved with status=resolved, paged: title, impact (degraded, down, maintenance), status (scheduled, investigating, identified, monitoring, in_progress, resolved), maintenance window, components and the full update thread.",
		Annotations: readOnlyHints(),
	}, s.listIncidentsTool)
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "list_alerts",
		Description: "List alerts, open by default or resolved with status=resolved (up to 200, most severe then most recent first), with the counters: kind, severity (attention, danger), the object (machine, service, heartbeat, probe, volume), details such as percent, mount point, exit code or reason, and whether it is acknowledged or silenced.",
		Annotations: readOnlyHints(),
	}, s.listAlertsTool)
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "get_alert",
		Description: "One alert with its deliveries to channels (event, status, attempts, failure reason, HTTP code).",
		Annotations: readOnlyHints(),
	}, s.getAlertTool)
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "list_alert_channels",
		Description: "List notification channels (webhooks): name, format (json, text, discord, slack, email, matrix), minimum severity, whether resolutions are sent, enabled, whether the URL is plain http, whether a signing secret is set. The url is reduced to its scheme and host: a webhook URL can itself be a secret.",
		Annotations: readOnlyHints(),
	}, s.listAlertChannelsTool)
}

// --- Entrées ---

type emptyInput struct{}

type machineInput struct {
	MachineID string `json:"machine_id" jsonschema:"Machine id, or 'local' for the openCloud host itself"`
}

type listServicesInput struct {
	MachineID string `json:"machine_id,omitempty" jsonschema:"Only the services of this machine"`
	Group     string `json:"group,omitempty" jsonschema:"Only the services of this Compose project"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Page size, default 100, max 500"`
	Offset    int    `json:"offset,omitempty" jsonschema:"Items to skip, default 0"`
}

type listDeclaredServicesInput struct {
	MachineID string `json:"machine_id,omitempty" jsonschema:"Only the services declared on this machine"`
	ProjectID string `json:"project_id,omitempty" jsonschema:"Only the services of this project; the undeclared containers are then left out, they belong to no project"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Page size, default 100, max 500"`
	Offset    int    `json:"offset,omitempty" jsonschema:"Items to skip, default 0"`
}

type serviceInput struct {
	ServiceID string `json:"service_id" jsonschema:"Service id, as returned by list_services"`
}

type serviceLogsInput struct {
	ServiceID string `json:"service_id" jsonschema:"Service id, as returned by list_services"`
	Tail      int    `json:"tail,omitempty" jsonschema:"Number of trailing lines, default 100, max 500"`
}

type pageInput struct {
	Limit  int `json:"limit,omitempty" jsonschema:"Page size, default 100, max 500"`
	Offset int `json:"offset,omitempty" jsonschema:"Items to skip, default 0"`
}

type jobInput struct {
	JobID string `json:"job_id" jsonschema:"Job id, as returned by list_jobs"`
}

type listProbesInput struct {
	MachineID string `json:"machine_id,omitempty" jsonschema:"Only the probes run by this machine"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Page size, default 100, max 500"`
	Offset    int    `json:"offset,omitempty" jsonschema:"Items to skip, default 0"`
}

type probeInput struct {
	ProbeID string `json:"probe_id" jsonschema:"Probe id, as returned by list_probes"`
}

type listIncidentsInput struct {
	Status string `json:"status,omitempty" jsonschema:"'open' (default) or 'resolved'"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Page size, default 100, max 500"`
	Offset int    `json:"offset,omitempty" jsonschema:"Items to skip, default 0"`
}

type listAlertsInput struct {
	Status string `json:"status,omitempty" jsonschema:"'open' (default) or 'resolved'"`
}

type alertInput struct {
	AlertID int64 `json:"alert_id" jsonschema:"Alert id, as returned by list_alerts"`
}

// --- Sorties propres aux outils : l'enveloppe change, les faits non ---

type overviewToolResponse struct {
	Version string         `json:"version"`
	Counts  countsResponse `json:"counts"`
}

type machinesToolResponse struct {
	Machines []machineJSON `json:"machines"`
}

type machineToolResponse struct {
	Machine   machineJSON `json:"machine"`
	Resources currentJSON `json:"resources"`
	Engine    *engineJSON `json:"engine"`
}

type servicesToolResponse struct {
	Services []serviceJSON `json:"services"`
	Engines  []engineJSON  `json:"engines"`
	Total    int           `json:"total"`
	Offset   int           `json:"offset"`
	Limit    int           `json:"limit"`
}

// Un service déclaré et son écart, côte à côte : l'écart n'est pas stocké.
type declaredServiceGapJSON struct {
	declaredServiceJSON
	Gap serviceGapJSON `json:"gap"`
}

type declaredServicesToolResponse struct {
	Services   []declaredServiceGapJSON `json:"services"`
	Undeclared []undeclaredJSON         `json:"undeclared"`
	Total      int                      `json:"total"`
	Offset     int                      `json:"offset"`
	Limit      int                      `json:"limit"`
}

type jobsToolResponse struct {
	Jobs   []jobJSON `json:"jobs"`
	Total  int       `json:"total"`
	Offset int       `json:"offset"`
	Limit  int       `json:"limit"`
}

type jobToolResponse struct {
	Job   jobJSON    `json:"job"`
	Runs  []runJSON  `json:"runs"`
	Pings []pingJSON `json:"pings"`
}

type probesToolResponse struct {
	Probes []probeJSON          `json:"probes"`
	Days   map[string][]dayJSON `json:"days"`
	Total  int                  `json:"total"`
	Offset int                  `json:"offset"`
	Limit  int                  `json:"limit"`
}

type statusPageToolResponse struct {
	Global     string          `json:"global"`
	Components []componentJSON `json:"components"`
	Incidents  []incidentJSON  `json:"incidents"`
}

type incidentsToolResponse struct {
	Incidents []incidentJSON `json:"incidents"`
	Total     int            `json:"total"`
	Offset    int            `json:"offset"`
	Limit     int            `json:"limit"`
}

// --- Handlers ---

func (s *Server) getOverviewTool(ctx context.Context, _ *gomcp.CallToolRequest, _ emptyInput) (*gomcp.CallToolResult, any, error) {
	counts, err := s.countsOf(ctx)
	if err != nil {
		return s.toolInternal("get_overview", err)
	}
	return toolJSON(overviewToolResponse{Version: s.version, Counts: counts})
}

func (s *Server) listMachinesTool(ctx context.Context, _ *gomcp.CallToolRequest, _ emptyInput) (*gomcp.CallToolResult, any, error) {
	statuses, err := s.machines.List(ctx)
	if err != nil {
		return s.toolInternal("list_machines", err)
	}
	withLifecycle, err := s.lifecycleEnabled(ctx)
	if err != nil {
		return s.toolInternal("list_machines", err)
	}
	response := machinesToolResponse{Machines: make([]machineJSON, 0, len(statuses))}
	for _, status := range statuses {
		response.Machines = append(response.Machines, s.machineJSONOf(status, withLifecycle))
	}
	return toolJSON(response)
}

func (s *Server) getMachineTool(ctx context.Context, _ *gomcp.CallToolRequest, input machineInput) (*gomcp.CallToolResult, any, error) {
	found, err := s.machines.Get(ctx, input.MachineID)
	if errors.Is(err, machine.ErrNotFound) {
		return toolRefuse(codeNotFound)
	}
	if err != nil {
		return s.toolInternal("get_machine", err)
	}
	current, err := s.resources.Current(ctx, found.ID)
	if err != nil {
		return s.toolInternal("get_machine", err)
	}
	engines, err := s.services.Engines(ctx)
	if err != nil {
		return s.toolInternal("get_machine", err)
	}
	withLifecycle, err := s.lifecycleEnabled(ctx)
	if err != nil {
		return s.toolInternal("get_machine", err)
	}
	response := machineToolResponse{Machine: s.machineJSONOf(found, withLifecycle), Resources: currentToJSON(current)}
	for _, engine := range engines {
		if engine.MachineID == found.ID {
			converted := engineToJSON(engine)
			response.Engine = &converted
		}
	}
	return toolJSON(response)
}

func (s *Server) getMachineNetworkTool(ctx context.Context, _ *gomcp.CallToolRequest, input machineInput) (*gomcp.CallToolResult, any, error) {
	found, err := s.machines.Get(ctx, input.MachineID)
	if errors.Is(err, machine.ErrNotFound) {
		return toolRefuse(codeNotFound)
	}
	if err != nil {
		return s.toolInternal("get_machine_network", err)
	}
	response, err := s.networkOf(ctx, found)
	if err != nil {
		return s.toolInternal("get_machine_network", err)
	}
	return toolJSON(response)
}

func (s *Server) listServicesTool(ctx context.Context, _ *gomcp.CallToolRequest, input listServicesInput) (*gomcp.CallToolResult, any, error) {
	services, err := s.services.List(ctx, input.MachineID)
	if err != nil {
		return s.toolInternal("list_services", err)
	}
	names, err := s.machineNames(ctx)
	if err != nil {
		return s.toolInternal("list_services", err)
	}
	currents, err := s.currentByService(ctx)
	if err != nil {
		return s.toolInternal("list_services", err)
	}
	engines, err := s.services.Engines(ctx)
	if err != nil {
		return s.toolInternal("list_services", err)
	}
	checks, err := s.updates.Checks(ctx, input.MachineID)
	if err != nil {
		return s.toolInternal("list_services", err)
	}
	kept := make([]service.Service, 0, len(services))
	for _, item := range services {
		if input.Group == "" || item.Group == input.Group {
			kept = append(kept, item)
		}
	}
	limit, offset := pageOf(input.Limit, input.Offset)
	response := servicesToolResponse{Services: []serviceJSON{}, Engines: []engineJSON{}, Total: len(kept), Offset: offset, Limit: limit}
	for _, item := range sliceOf(kept, limit, offset) {
		response.Services = append(response.Services, serviceToJSON(item, names[item.MachineID], currents[item.ID], checks))
	}
	for _, engine := range engines {
		if input.MachineID == "" || engine.MachineID == input.MachineID {
			response.Engines = append(response.Engines, engineToJSON(engine))
		}
	}
	return toolJSON(response)
}

func (s *Server) listDeclaredServicesTool(ctx context.Context, _ *gomcp.CallToolRequest, input listDeclaredServicesInput) (*gomcp.CallToolResult, any, error) {
	// Une seule lecture de l'annuaire : le rapport porte les services qu'il a
	// jugés, dans l'ordre de leurs écarts, donc chacun a le sien.
	report, err := s.directory.Gaps(ctx, directory.Filter{MachineID: input.MachineID, ProjectID: input.ProjectID})
	if err != nil {
		return s.toolInternal("list_declared_services", err)
	}
	gaps, err := s.gapsResponseOf(ctx, report)
	if err != nil {
		return s.toolInternal("list_declared_services", err)
	}
	limit, offset := pageOf(input.Limit, input.Offset)
	response := declaredServicesToolResponse{Services: []declaredServiceGapJSON{}, Undeclared: gaps.Undeclared, Total: len(report.Declared), Offset: offset, Limit: limit}
	for index, found := range report.Declared {
		if index < offset || index >= offset+limit {
			continue
		}
		response.Services = append(response.Services, declaredServiceGapJSON{declaredServiceJSON: declaredServiceToJSON(found), Gap: gaps.Services[index]})
	}
	return toolJSON(response)
}

func (s *Server) getServiceTool(ctx context.Context, _ *gomcp.CallToolRequest, input serviceInput) (*gomcp.CallToolResult, any, error) {
	item, err := s.services.Get(ctx, input.ServiceID)
	if errors.Is(err, service.ErrNotFound) {
		return toolRefuse(codeNotFound)
	}
	if err != nil {
		return s.toolInternal("get_service", err)
	}
	response, err := s.serviceResponseOf(ctx, item)
	if err != nil {
		return s.toolInternal("get_service", err)
	}
	return toolJSON(response)
}

func (s *Server) getServiceLogsTool(ctx context.Context, _ *gomcp.CallToolRequest, input serviceLogsInput) (*gomcp.CallToolResult, any, error) {
	item, err := s.services.Get(ctx, input.ServiceID)
	if errors.Is(err, service.ErrNotFound) {
		return toolRefuse(codeNotFound)
	}
	if err != nil {
		return s.toolInternal("get_service_logs", err)
	}
	if !s.takeLogSlot() {
		return toolRefuse(codeLogsBusy)
	}
	defer s.releaseLogSlot()
	lines, err := s.services.FetchLogs(ctx, item.ID, input.Tail)
	if err != nil {
		if code, _, refused := logsRefusal(err); refused {
			return toolRefuse(code)
		}
		return s.toolInternal("get_service_logs", err)
	}
	response := logsResponse{Lines: make([]logLineJSON, 0, len(lines))}
	for _, line := range lines {
		response.Lines = append(response.Lines, logLineToJSON(line))
	}
	return toolJSON(response)
}

func (s *Server) listJobsTool(ctx context.Context, _ *gomcp.CallToolRequest, input pageInput) (*gomcp.CallToolResult, any, error) {
	heartbeats, err := s.heartbeats.List(ctx)
	if err != nil {
		return s.toolInternal("list_jobs", err)
	}
	limit, offset := pageOf(input.Limit, input.Offset)
	response := jobsToolResponse{Jobs: []jobJSON{}, Total: len(heartbeats), Offset: offset, Limit: limit}
	for _, found := range sliceOf(heartbeats, limit, offset) {
		response.Jobs = append(response.Jobs, jobToJSON(found))
	}
	return toolJSON(response)
}

func (s *Server) getJobTool(ctx context.Context, _ *gomcp.CallToolRequest, input jobInput) (*gomcp.CallToolResult, any, error) {
	found, err := s.heartbeats.Get(ctx, input.JobID)
	if errors.Is(err, heartbeat.ErrNotFound) {
		return toolRefuse(codeNotFound)
	}
	if err != nil {
		return s.toolInternal("get_job", err)
	}
	runs, err := s.heartbeats.Runs(ctx, found.ID, jobRunsShown)
	if err != nil {
		return s.toolInternal("get_job", err)
	}
	pings, err := s.heartbeats.Pings(ctx, found.ID, jobPingsShown)
	if err != nil {
		return s.toolInternal("get_job", err)
	}
	response := jobToolResponse{Job: jobToJSON(found), Runs: make([]runJSON, 0, len(runs)), Pings: make([]pingJSON, 0, len(pings))}
	for _, run := range runs {
		response.Runs = append(response.Runs, runToJSON(run))
	}
	for _, ping := range pings {
		response.Pings = append(response.Pings, pingToJSON(ping))
	}
	return toolJSON(response)
}

func (s *Server) listProbesTool(ctx context.Context, _ *gomcp.CallToolRequest, input listProbesInput) (*gomcp.CallToolResult, any, error) {
	probes, err := s.probes.List(ctx, input.MachineID)
	if err != nil {
		return s.toolInternal("list_probes", err)
	}
	online, err := s.onlineMachines(ctx)
	if err != nil {
		return s.toolInternal("list_probes", err)
	}
	days, err := s.probes.Days(ctx, "", probeListDays)
	if err != nil {
		return s.toolInternal("list_probes", err)
	}
	limit, offset := pageOf(input.Limit, input.Offset)
	response := probesToolResponse{Probes: []probeJSON{}, Days: map[string][]dayJSON{}, Total: len(probes), Offset: offset, Limit: limit}
	carried := map[string]bool{}
	for _, found := range sliceOf(probes, limit, offset) {
		carried[found.ID] = true
		response.Probes = append(response.Probes, probeToJSON(found, online[found.MachineID]))
	}
	for _, day := range days {
		if carried[day.ProbeID] {
			response.Days[day.ProbeID] = append(response.Days[day.ProbeID], dayToJSON(day))
		}
	}
	return toolJSON(response)
}

func (s *Server) getProbeTool(ctx context.Context, _ *gomcp.CallToolRequest, input probeInput) (*gomcp.CallToolResult, any, error) {
	found, err := s.probes.Get(ctx, input.ProbeID)
	if errors.Is(err, probe.ErrNotFound) {
		return toolRefuse(codeNotFound)
	}
	if err != nil {
		return s.toolInternal("get_probe", err)
	}
	response, err := s.probeResponseOf(ctx, found)
	if err != nil {
		return s.toolInternal("get_probe", err)
	}
	return toolJSON(response)
}

func (s *Server) getStatusPageTool(ctx context.Context, _ *gomcp.CallToolRequest, _ emptyInput) (*gomcp.CallToolResult, any, error) {
	components, err := s.status.Components(ctx)
	if err != nil {
		return s.toolInternal("get_status_page", err)
	}
	incidents, err := s.status.Incidents(ctx)
	if err != nil {
		return s.toolInternal("get_status_page", err)
	}
	response := statusPageToolResponse{Components: make([]componentJSON, 0, len(components)), Incidents: []incidentJSON{}}
	states := []status.State{}
	for _, component := range components {
		if component.Effective != status.StateUnknown {
			states = append(states, component.Effective)
		}
		response.Components = append(response.Components, componentToJSON(component))
	}
	response.Global = string(status.Worst(states))
	for _, incident := range incidents {
		if !incident.IsResolved() {
			response.Incidents = append(response.Incidents, incidentToJSON(incident))
		}
	}
	return toolJSON(response)
}

func (s *Server) listIncidentsTool(ctx context.Context, _ *gomcp.CallToolRequest, input listIncidentsInput) (*gomcp.CallToolResult, any, error) {
	incidents, err := s.status.Incidents(ctx)
	if err != nil {
		return s.toolInternal("list_incidents", err)
	}
	wantResolved := input.Status == string(status.StatusResolved)
	kept := make([]status.Incident, 0, len(incidents))
	for _, incident := range incidents {
		if incident.IsResolved() == wantResolved {
			kept = append(kept, incident)
		}
	}
	limit, offset := pageOf(input.Limit, input.Offset)
	response := incidentsToolResponse{Incidents: []incidentJSON{}, Total: len(kept), Offset: offset, Limit: limit}
	for _, incident := range sliceOf(kept, limit, offset) {
		response.Incidents = append(response.Incidents, incidentToJSON(incident))
	}
	return toolJSON(response)
}

func (s *Server) listAlertsTool(ctx context.Context, _ *gomcp.CallToolRequest, input listAlertsInput) (*gomcp.CallToolResult, any, error) {
	wanted := alert.StatusOpen
	if input.Status == string(alert.StatusResolved) {
		wanted = alert.StatusResolved
	}
	alerts, err := s.alerts.List(ctx, wanted)
	if err != nil {
		return s.toolInternal("list_alerts", err)
	}
	counts, err := s.alerts.Count(ctx)
	if err != nil {
		return s.toolInternal("list_alerts", err)
	}
	response := alertsResponse{Alerts: make([]alertJSON, 0, len(alerts)), Counts: alertCountsJSON{Open: counts.Open, Unacknowledged: counts.Unacknowledged}}
	for _, found := range alerts {
		response.Alerts = append(response.Alerts, alertToJSON(found))
	}
	return toolJSON(response)
}

func (s *Server) getAlertTool(ctx context.Context, _ *gomcp.CallToolRequest, input alertInput) (*gomcp.CallToolResult, any, error) {
	found, err := s.alerts.Get(ctx, input.AlertID)
	if errors.Is(err, alert.ErrNotFound) {
		return toolRefuse(codeNotFound)
	}
	if err != nil {
		return s.toolInternal("get_alert", err)
	}
	deliveries, err := s.alerts.Deliveries(ctx, found.ID)
	if err != nil {
		return s.toolInternal("get_alert", err)
	}
	return toolJSON(alertResponseOf(found, deliveries))
}

func (s *Server) listAlertChannelsTool(ctx context.Context, _ *gomcp.CallToolRequest, _ emptyInput) (*gomcp.CallToolResult, any, error) {
	channels, err := s.alerts.Channels(ctx)
	if err != nil {
		return s.toolInternal("list_alert_channels", err)
	}
	response := channelsResponse{Channels: make([]channelJSON, 0, len(channels))}
	for _, channel := range channels {
		converted := channelToJSON(channel)
		converted.URL = originOf(channel.URL)
		// Des adresses ou un salon : rien dont un agent ait besoin.
		converted.Destination = ""
		response.Channels = append(response.Channels, converted)
	}
	return toolJSON(response)
}

// originOf réduit une URL à son schéma et son hôte : le chemin d'un
// webhook Discord est son jeton.
func originOf(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}
