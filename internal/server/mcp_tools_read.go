package server

import (
	"context"
	"net/url"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ldesfontaine/opencloud/internal/alert"
	"github.com/ldesfontaine/opencloud/internal/audit"
	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/container"
	"github.com/ldesfontaine/opencloud/internal/directory"
	"github.com/ldesfontaine/opencloud/internal/heartbeat"
	"github.com/ldesfontaine/opencloud/internal/machine"
	"github.com/ldesfontaine/opencloud/internal/manifest"
	"github.com/ldesfontaine/opencloud/internal/probe"
	"github.com/ldesfontaine/opencloud/internal/server/httpx"
	"github.com/ldesfontaine/opencloud/internal/status"
)

// Les dix-neuf lectures : chacune rend les faits de l'API, sous le même JSON.
// Aucun secret d'openCloud n'en sort : ni l'URL de ping d'une tâche, ni les
// jetons en attente d'une machine, ni l'URL complète d'un canal. Les
// journaux d'un conteneur, eux, sont bruts et peuvent en contenir un.
func (s *Server) registerReadTools(server *gomcp.Server) {
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "get_overview",
		Description: brand.ProductName + " version and the counters of the overview: machines (total, online), jobs and probes (total, needing attention), containers (total, needing attention, with a newer image), certificates (total, expiring, expired, soonest expiry), open incidents, alerts (open, unacknowledged).",
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
		Description: "The Docker network topology of one machine: its containers with their exposure findings, the networks grouping them, and the edges from the Internet to published ports and between dependent containers.",
		Annotations: readOnlyHints(),
	}, s.getMachineNetworkTool)
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "list_containers",
		Description: "List Docker containers across machines, optionally filtered by machine_id or Compose project (group), paged. Each container carries its state, exit code, health, restart count, image, published ports, exposure findings, the latest image check (a newer tag or digest) and its current CPU/memory sample. Derive 'failing' from state=exited with exit_code!=0 or health=unhealthy.",
		Annotations: readOnlyHints(),
	}, s.listContainersTool)
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "get_container",
		Description: "One container with the Docker engine of its machine and its last 50 state transitions (start, die, health changes, with the raw log snippet captured on an abnormal exit, which can contain secrets).",
		Annotations: readOnlyHints(),
	}, s.getContainerTool)
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "list_declared_services",
		Description: "Declared services of the directory with their gap against what Docker shows (conform, absent, diverged, unverifiable on latest or untagged, unobserved for binaries, weak name match, strong label match), and the containers present but undeclared, grouped by Compose project or name, flagged when ignored or when their label is orphan.",
		Annotations: readOnlyHints(),
	}, s.listDeclaredServicesTool)
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "export_directory",
		Description: "The whole directory as the YAML file the " + brand.Command + " export command writes: projects, declared services and their units, probes, jobs, ignored keys. Ids included, never a secret (a job's token is only the marker `secret: kept`, and a password in a probe target is masked as `user:…@host`: do not apply this file as is). With propose=true, entries proposed from the undeclared containers that are not ignored are added, without ids, for the operator to review before apply. Returns {yaml}.",
		Annotations: readOnlyHints(),
	}, s.exportDirectoryTool)
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "get_container_logs",
		Description: "The last lines of a container's stdout/stderr, fetched live from the machine's agent (tail 1 to 500, default 100). Fails with container.machine_offline when the agent is not connected. Raw container output, not filtered: it can contain secrets and is sent to whoever runs the model. Over stdio (" + brand.Command + " mcp) the agents' streams are not held: it always fails with container.logs_unavailable_locally, use the HTTP transport.",
		Annotations: readOnlyHints(),
	}, s.getContainerLogsTool)
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
		Description: "List HTTP and TCP probes, optionally filtered by machine_id, paged: status (new, up, degraded, down, paused), target (a password in it is masked), machine that probes and whether it is online, interval, thresholds, last check with its reason, the TLS certificate last seen (subject, issuer, validity dates, fingerprint, chain_valid, hostname_match, OCSP), and per-probe daily counts over 30 days to compute uptime.",
		Annotations: readOnlyHints(),
	}, s.listProbesTool)
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "get_probe",
		Description: "One probe (its target's password masked) with its uptime windows (total and successful checks per window), 90 days of daily counts, and its last 50 checks (outcome, duration, HTTP code, reason).",
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
		Description: "List alerts, open by default or resolved with status=resolved (up to 200, most severe then most recent first), with the counters: kind, severity (attention, danger), the object (machine, service, heartbeat, probe, volume; a service object is a container), details such as percent, mount point, exit code or reason, and whether it is acknowledged or silenced.",
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
	gomcp.AddTool(server, &gomcp.Tool{
		Name:        "list_audit",
		Description: "The audit journal, most recent first (up to 200 per page, default 50; pass next_before as before for the next page), optionally for one object (object_kind, object_id). Each event says who acted (actor_kind: operator, mcp, cli, rule for a background loop, agent, or unknown when a gesture ran without an actor; actor_name: the operator identity, the API token id or OAuth family id, the system account, the machine id), the action (object_kind.verb), the object (kind, id, name copied at the time), details without any secret, and apply_id when an apply wrote it. Observations (signals, measures, probe results) and reads are not journaled. Events are kept one year.",
		Annotations: readOnlyHints(),
	}, s.listAuditTool)
}

// --- Entrées ---

type emptyInput struct{}

type listAuditInput struct {
	Limit      int    `json:"limit,omitempty" jsonschema:"Page size, default 50, max 200"`
	Before     int64  `json:"before,omitempty" jsonschema:"Event id to continue below: the next_before of the previous page"`
	ObjectKind string `json:"object_kind,omitempty" jsonschema:"Only the events on this kind of object, such as probe, service, channel or mcp_token"`
	ObjectID   string `json:"object_id,omitempty" jsonschema:"Only the events on this object id"`
}

type machineInput struct {
	MachineID string `json:"machine_id" jsonschema:"Machine id, or 'local' for the openCloud host itself"`
}

type listContainersInput struct {
	MachineID string `json:"machine_id,omitempty" jsonschema:"Only the containers of this machine"`
	Group     string `json:"group,omitempty" jsonschema:"Only the containers of this Compose project"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Page size, default 100, max 500"`
	Offset    int    `json:"offset,omitempty" jsonschema:"Items to skip, default 0"`
}

type listDeclaredServicesInput struct {
	MachineID string `json:"machine_id,omitempty" jsonschema:"Only the services declared on this machine"`
	ProjectID string `json:"project_id,omitempty" jsonschema:"Only the services of this project; the undeclared containers are then left out, they belong to no project"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Page size, default 100, max 500"`
	Offset    int    `json:"offset,omitempty" jsonschema:"Items to skip, default 0"`
}

type containerInput struct {
	ID string `json:"id" jsonschema:"Container id: the id field returned by list_containers, not its container_id (the Docker id)"`
}

type containerLogsInput struct {
	ID   string `json:"id" jsonschema:"Container id: the id field returned by list_containers, not its container_id (the Docker id)"`
	Tail int    `json:"tail,omitempty" jsonschema:"Number of trailing lines, default 100, max 500"`
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

type containersToolResponse struct {
	Containers []containerJSON `json:"containers"`
	Engines    []engineJSON    `json:"engines"`
	Total      int             `json:"total"`
	Offset     int             `json:"offset"`
	Limit      int             `json:"limit"`
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
	machines := httpx.ConvertAll(statuses, func(machineStatus machine.Status) machineJSON {
		return s.machineJSONOf(machineStatus, withLifecycle)
	})
	return toolJSON(machinesToolResponse{Machines: machines})
}

func (s *Server) getMachineTool(ctx context.Context, _ *gomcp.CallToolRequest, input machineInput) (*gomcp.CallToolResult, any, error) {
	found, err := s.machines.Get(ctx, input.MachineID)
	if result, done := s.toolFailed("get_machine", err, machine.ErrNotFound); done {
		return result, nil, nil
	}
	current, err := s.resources.Current(ctx, found.ID)
	if err != nil {
		return s.toolInternal("get_machine", err)
	}
	engines, err := s.reader.ListEngines(ctx)
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
	if result, done := s.toolFailed("get_machine_network", err, machine.ErrNotFound); done {
		return result, nil, nil
	}
	response, err := s.networkOf(ctx, found)
	if err != nil {
		return s.toolInternal("get_machine_network", err)
	}
	return toolJSON(response)
}

func (s *Server) listContainersTool(ctx context.Context, _ *gomcp.CallToolRequest, input listContainersInput) (*gomcp.CallToolResult, any, error) {
	listing, err := s.containerListOf(ctx, input.MachineID)
	if err != nil {
		return s.toolInternal("list_containers", err)
	}
	kept := make([]containerJSON, 0, len(listing.Containers))
	for _, item := range listing.Containers {
		if input.Group == "" || item.Group == input.Group {
			kept = append(kept, item)
		}
	}
	limit, offset := pageOf(input.Limit, input.Offset)
	response := containersToolResponse{Containers: sliceOf(kept, limit, offset), Engines: listing.Engines, Total: len(kept), Offset: offset, Limit: limit}
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

type exportDirectoryResponse struct {
	YAML string `json:"yaml"`
}

type exportDirectoryInput struct {
	Propose bool `json:"propose,omitempty" jsonschema:"Add entries proposed from the undeclared containers that are not ignored, without ids, for the operator to review before apply"`
}

func (s *Server) exportDirectoryTool(ctx context.Context, _ *gomcp.CallToolRequest, input exportDirectoryInput) (*gomcp.CallToolResult, any, error) {
	data, err := s.exportedDirectory(ctx, manifest.ExportOptions{Propose: input.Propose, RedactSecrets: true})
	if result, done := s.toolFailed("export_directory", err, nil); done {
		return result, nil, nil
	}
	return toolJSON(exportDirectoryResponse{YAML: string(data)})
}

func (s *Server) getContainerTool(ctx context.Context, _ *gomcp.CallToolRequest, input containerInput) (*gomcp.CallToolResult, any, error) {
	item, err := s.reader.GetContainer(ctx, input.ID)
	if result, done := s.toolFailed("get_container", err, container.ErrNotFound); done {
		return result, nil, nil
	}
	response, err := s.containerResponseOf(ctx, item)
	if err != nil {
		return s.toolInternal("get_container", err)
	}
	return toolJSON(response)
}

func (s *Server) getContainerLogsTool(ctx context.Context, _ *gomcp.CallToolRequest, input containerLogsInput) (*gomcp.CallToolResult, any, error) {
	item, err := s.reader.GetContainer(ctx, input.ID)
	if result, done := s.toolFailed("get_container_logs", err, container.ErrNotFound); done {
		return result, nil, nil
	}
	if !s.takeLogSlot() {
		return toolRefuse(codeLogsBusy)
	}
	defer s.releaseLogSlot()
	lines, err := s.containers.FetchLogs(ctx, item.ID, input.Tail)
	if err != nil {
		if code, _, refused := logsRefusal(err); refused {
			return toolRefuse(code)
		}
		return s.toolInternal("get_container_logs", err)
	}
	response := logsResponse{Lines: httpx.ConvertAll(lines, logLineToJSON)}
	return toolJSON(response)
}

func (s *Server) listJobsTool(ctx context.Context, _ *gomcp.CallToolRequest, input pageInput) (*gomcp.CallToolResult, any, error) {
	heartbeats, err := s.reader.ListHeartbeats(ctx)
	if err != nil {
		return s.toolInternal("list_jobs", err)
	}
	limit, offset := pageOf(input.Limit, input.Offset)
	response := jobsToolResponse{Jobs: httpx.ConvertAll(sliceOf(heartbeats, limit, offset), jobToJSON), Total: len(heartbeats), Offset: offset, Limit: limit}
	return toolJSON(response)
}

func (s *Server) getJobTool(ctx context.Context, _ *gomcp.CallToolRequest, input jobInput) (*gomcp.CallToolResult, any, error) {
	found, err := s.reader.GetHeartbeat(ctx, input.JobID)
	if result, done := s.toolFailed("get_job", err, heartbeat.ErrNotFound); done {
		return result, nil, nil
	}
	runs, err := s.reader.ListRuns(ctx, found.ID, jobRunsShown)
	if err != nil {
		return s.toolInternal("get_job", err)
	}
	pings, err := s.reader.ListPings(ctx, found.ID, jobPingsShown)
	if err != nil {
		return s.toolInternal("get_job", err)
	}
	response := jobToolResponse{Job: jobToJSON(found), Runs: httpx.ConvertAll(runs, runToJSON), Pings: httpx.ConvertAll(pings, pingToJSON)}
	return toolJSON(response)
}

// mcpProbeJSON est probeToJSON pour un client MCP : la cible y perd son mot
// de passe, comme dans le plan d'apply et les alertes. La description des
// outils promet qu'aucun secret n'en sort.
func mcpProbeJSON(found probe.Probe, online bool) probeJSON {
	return redactedProbe(probeToJSON(found, online))
}

// redactedProbe est la seule règle « MCP masque la cible » : la liste et la
// fiche d'une sonde passent par elle.
func redactedProbe(converted probeJSON) probeJSON {
	converted.Target = probe.RedactTarget(converted.Target)
	return converted
}

func (s *Server) listProbesTool(ctx context.Context, _ *gomcp.CallToolRequest, input listProbesInput) (*gomcp.CallToolResult, any, error) {
	listing, err := s.probeListOf(ctx, input.MachineID)
	if err != nil {
		return s.toolInternal("list_probes", err)
	}
	limit, offset := pageOf(input.Limit, input.Offset)
	response := probesToolResponse{Probes: sliceOf(listing.Probes, limit, offset), Days: map[string][]dayJSON{}, Total: len(listing.Probes), Offset: offset, Limit: limit}
	for index, found := range response.Probes {
		response.Probes[index] = redactedProbe(found)
		if days, carried := listing.Days[found.ID]; carried {
			response.Days[found.ID] = days
		}
	}
	return toolJSON(response)
}

func (s *Server) getProbeTool(ctx context.Context, _ *gomcp.CallToolRequest, input probeInput) (*gomcp.CallToolResult, any, error) {
	found, err := s.reader.GetProbe(ctx, input.ProbeID)
	if result, done := s.toolFailed("get_probe", err, probe.ErrNotFound); done {
		return result, nil, nil
	}
	response, err := s.probeResponseOf(ctx, found)
	if err != nil {
		return s.toolInternal("get_probe", err)
	}
	response.Probe = redactedProbe(response.Probe)
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
	response := incidentsToolResponse{Incidents: httpx.ConvertAll(sliceOf(kept, limit, offset), incidentToJSON), Total: len(kept), Offset: offset, Limit: limit}
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
	counts, err := s.reader.CountAlerts(ctx)
	if err != nil {
		return s.toolInternal("list_alerts", err)
	}
	response := alertsResponse{Alerts: httpx.ConvertAll(alerts, alertToJSON), Counts: alertCountsJSON{Open: counts.Open, Unacknowledged: counts.Unacknowledged}}
	return toolJSON(response)
}

func (s *Server) getAlertTool(ctx context.Context, _ *gomcp.CallToolRequest, input alertInput) (*gomcp.CallToolResult, any, error) {
	found, err := s.reader.GetAlert(ctx, input.AlertID)
	if result, done := s.toolFailed("get_alert", err, alert.ErrNotFound); done {
		return result, nil, nil
	}
	deliveries, err := s.reader.ListDeliveries(ctx, found.ID)
	if err != nil {
		return s.toolInternal("get_alert", err)
	}
	return toolJSON(alertResponseOf(found, deliveries))
}

func (s *Server) listAlertChannelsTool(ctx context.Context, _ *gomcp.CallToolRequest, _ emptyInput) (*gomcp.CallToolResult, any, error) {
	channels, err := s.reader.ListChannels(ctx)
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

func (s *Server) listAuditTool(ctx context.Context, _ *gomcp.CallToolRequest, input listAuditInput) (*gomcp.CallToolResult, any, error) {
	query := audit.Query{Limit: input.Limit, Before: input.Before, ObjectKind: audit.ObjectKind(input.ObjectKind), ObjectID: input.ObjectID}
	if !isKnownObjectKind(query.ObjectKind) {
		return toolRefuse(codeBadQuery)
	}
	response, err := s.auditPage(ctx, query)
	if err != nil {
		return s.toolInternal("list_audit", err)
	}
	return toolJSON(response)
}
