import { expect, test } from "vitest";

import type { ActionsResponse, ActionRun, RunLine } from "./actions";
import type { AlertResponse, AlertsResponse, ChannelsResponse, MutesResponse } from "./alerts";
import type { AuditForwarding, AuditResponse } from "./audit";
import type { BackupDestination, BackupKeyring, BackupPlan, Backups } from "./backups";
import type { AuthState } from "./auth";
import type { ContainerResponse, ContainersResponse } from "./containers";
import type { Counts } from "./counts";
import type { DeletionCount, DeletionPlan } from "./deletion";
import type { DeclaredServiceDetail, DeclaredServicesResponse, GapsResponse, ProjectsResponse, ProposalResponse } from "./directory";
import type { JobResponse, JobsResponse } from "./jobs";
import type { Agent, Capability, Machine, MachinesResponse } from "./machines";
import type { AppliesResponse, Apply } from "./manifest";
import type { MCPState } from "./mcp";
import type { NetworkResponse, OverviewNetworkResponse } from "./network";
import type { ProbeResponse, ProbesResponse } from "./probes";
import type { Current, HistoryResponse, ResourcesResponse } from "./resources";
import type { Session } from "./session";
import type { Incident, IncidentsResponse, PublicStatus, StatusComponent, StatusComponentsResponse, StatusPagesResponse } from "./status";

// Le contrat de l'API est écrit deux fois : en Go (les balises json) et ici
// (les types de api/*.ts). Les réponses figées par les goldens du serveur
// (internal/server/testdata) font foi : ce test les relit et compare leurs
// clés à la description de chaque type. tsc vérifie que la description a
// exactement les clés du type, optionnelles comprises ; vitest, que le
// golden a exactement les clés de la description.

type Scalar = string | number | boolean | null | undefined;

// Un objet décrit : son nom pour le message, et une description par clé.
interface Shape<T> {
  name: string;
  fields: Record<string, Field>;
  // Fantôme : relie la description à son type, pour que tsc compare.
  readonly described?: T;
}

// Un dictionnaire (Record<string, …>) : ses clés sont des données, seules ses
// valeurs se décrivent.
interface Dictionary {
  dictionaryOf: Spec;
}

// Ce qu'une valeur porte : rien à décrire pour un scalaire ou une liste de
// scalaires ; un objet, une liste d'objets ou un dictionnaire ont la leur.
type Spec = "value" | Shape<unknown> | Dictionary;

// Une clé que le serveur peut omettre (omitempty).
interface Optional {
  optional: Spec;
}

type Field = Spec | Optional;

type SpecOf<V> = [NonNullable<V>] extends [Scalar]
  ? "value"
  : [NonNullable<V>] extends [readonly (infer Item)[]]
    ? [NonNullable<Item>] extends [Scalar]
      ? "value"
      : Shape<NonNullable<Item>>
    : string extends keyof NonNullable<V>
      ? Dictionary
      : Shape<NonNullable<V>>;

// Une clé optionnelle du type s'écrit optional(…), une clé obligatoire non.
type FieldsOf<T> = {
  [K in keyof T]-?: {} extends Pick<T, K> ? { optional: SpecOf<T[K]> } : SpecOf<T[K]>;
};

function shape<T>(name: string, fields: FieldsOf<NoInfer<T>>): Shape<T> {
  return { name, fields: fields as Record<string, Field> };
}

function optional<S extends Spec>(spec: S): { optional: S } {
  return { optional: spec };
}

function dictionaryOf(spec: Spec): Dictionary {
  return { dictionaryOf: spec };
}

const value = "value";

// Les briques partagées, de la plus petite à la plus grande.

const lifecycle = shape<NonNullable<Machine["lifecycle"]>>("Lifecycle", {
  status: value,
  product: value,
  product_url: value,
  cycle: value,
  eol_from: value,
  latest: value,
  newer: shape("NewerCycle", { cycle: value, latest: value, eol_from: value }),
  fetched_at: value,
  is_stale: value,
});

const capability = shape<Capability>("Capability", { name: value, paths: value });

const machine = shape<Machine>("Machine", {
  id: value,
  name: value,
  hostname: value,
  address: value,
  os: value,
  arch: value,
  online: value,
  last_seen_at: value,
  agent: shape<Agent>("Agent", {
    id: value,
    version: value,
    enrolled_at: value,
    revoked_at: value,
    capabilities: capability,
    reported_at: value,
    requested: capability,
    server_key_fingerprint: value,
    capabilities_differ: value,
  }),
  lifecycle: optional(lifecycle),
});

const machines = shape<MachinesResponse>("MachinesResponse", {
  machines: machine,
  pending: shape("PendingToken", { id: value, name: value, masked: value, expires_at: value, reenroll: value }),
});

type ContainerJSON = ContainersResponse["containers"][number];
type EngineJSON = ContainersResponse["engines"][number];

const engine = shape<EngineJSON>("Engine", {
  machine_id: value,
  present: value,
  reason: value,
  version: value,
  api_version: value,
  checked_at: value,
});

const container = shape<ContainerJSON>("Container", {
  id: value,
  machine_id: value,
  machine_name: value,
  kind: value,
  name: value,
  group: value,
  container_id: value,
  image: value,
  image_id: value,
  compose_service: value,
  compose_dir: value,
  compose_file: value,
  update_policy: value,
  declared_service_id: value,
  image_check: shape("ImageCheck", {
    checked_at: value,
    outcome: value,
    local_digest: value,
    remote_digest: value,
    newer_tag: value,
    newer_digest: value,
    kind: value,
    target: value,
    command: value,
  }),
  lifecycle: optional(lifecycle),
  state: value,
  exit_code: value,
  oom_killed: value,
  killed: value,
  health: value,
  restart_count: value,
  ports: shape("Port", { ip: value, host_port: value, container_port: value, protocol: value }),
  network_mode: value,
  privileged: value,
  networks: shape("Attachment", { network_id: value, name: value, ip: value, aliases: value }),
  depends_on: shape("Dependency", { name: value, source: value }),
  exposure: shape("Finding", { kind: value, level: value, port: optional(value), protocol: optional(value) }),
  created_at: value,
  started_at: value,
  finished_at: value,
  first_seen_at: value,
  last_seen_at: value,
  archived_at: value,
  current: shape("ContainerSample", { sampled_at: value, cpu_percent: value, mem_used: value, mem_limit: value }),
});

const containers = shape<ContainersResponse>("ContainersResponse", { containers: container, engines: engine });

const containerResponse = shape<ContainerResponse>("ContainerResponse", {
  container,
  engine,
  transitions: shape("Transition", {
    id: value,
    at: value,
    action: value,
    previous_state: value,
    new_state: value,
    previous_health: value,
    new_health: value,
    exit_code: value,
    oom_killed: value,
    killed: value,
    replayed: value,
    snippet: value,
  }),
  directory: shape("ContainerDirectory", {
    declared: value,
    service_id: value,
    service_name: value,
    unit_id: value,
    unit_name: value,
    match_level: value,
    weak: value,
    key_kind: value,
    key_value: value,
    ignored: value,
    label_service: value,
    label_unit: value,
    label_problem: value,
    label_machine_name: value,
  }),
});

const network = shape<NetworkResponse>("NetworkResponse", {
  machine_id: value,
  machine_name: value,
  online: value,
  engine,
  containers: container,
  groups: shape("Group", { network_id: value, name: value, internal: value, members: value }),
  edges: shape("Edge", { from: value, to: value, kind: value, port: optional(value), protocol: optional(value), source: optional(value) }),
});

type ProbeJSON = ProbesResponse["probes"][number];
type ProbeDayJSON = ProbeResponse["days"][number];

const probe = shape<ProbeJSON>("Probe", {
  id: value,
  name: value,
  kind: value,
  target: value,
  machine_id: value,
  machine_name: value,
  machine_online: value,
  service_id: value,
  service_name: value,
  service_machine_name: value,
  status: value,
  interval_seconds: value,
  timeout_seconds: value,
  failure_threshold: value,
  recovery_threshold: value,
  method: value,
  expected_status: value,
  expected_body: value,
  follow_redirects: value,
  tls: value,
  last_checked_at: value,
  last_duration_ms: value,
  last_code: value,
  last_reason: value,
  certificate: shape("Certificate", {
    subject: value,
    issuer: value,
    not_before: value,
    not_after: value,
    fingerprint: value,
    chain_valid: value,
    hostname_match: value,
    ocsp: value,
  }),
  created_at: value,
});

const probeDay = shape<ProbeDayJSON>("ProbeDay", { day: value, total: value, success: value, degraded: value, duration_ms: value });

const probes = shape<ProbesResponse>("ProbesResponse", { probes: probe, days: dictionaryOf(probeDay) });

const probeResponse = shape<ProbeResponse>("ProbeResponse", {
  probe,
  uptime: shape("ProbeUptime", { window: value, total: value, success: value }),
  days: probeDay,
  results: shape("ProbeResult", { checked_at: value, outcome: value, duration_ms: value, code: value, reason: value }),
});

type JobJSON = JobsResponse["jobs"][number];

const job = shape<JobJSON>("Job", {
  id: value,
  name: value,
  machine_id: value,
  machine_name: value,
  status: value,
  interval_seconds: value,
  grace_seconds: value,
  last_ping_at: value,
  next_deadline_at: value,
  run_started_at: value,
  last_exit_code: value,
  last_duration_ms: value,
  created_at: value,
});

const jobs = shape<JobsResponse>("JobsResponse", { jobs: job });

const jobResponse = shape<JobResponse>("JobResponse", {
  job,
  ping_url: value,
  url_local: value,
  snippets: shape("Snippet", { key: value, code: value }),
  runs: shape("Run", { id: value, started_at: value, completed_at: value, duration_ms: value, exit_code: value, outcome: value, payload: value }),
  pings: shape("Ping", { id: value, kind: value, exit_code: value, source: value, method: value, received_at: value }),
});

type ReadingJSON = HistoryResponse["points"][number];

const reading = shape<ReadingJSON>("Reading", {
  sampled_at: value,
  cpu_percent: value,
  cpu_cores: value,
  load_1: value,
  mem_used: value,
  mem_total: value,
  swap_used: value,
  swap_total: value,
  disk_used: value,
  disk_total: value,
  net_rx_per_second: value,
  net_tx_per_second: value,
  disks: optional(shape("Disk", { mount_point: value, device: value, used: value, total: value })),
});

const current = shape<Current>("Current", { machine_id: value, available: value, sample: reading });

const history = shape<HistoryResponse>("HistoryResponse", {
  machine_id: value,
  window: value,
  span_seconds: value,
  step_seconds: value,
  points: reading,
});

type AlertJSON = AlertsResponse["alerts"][number];

const alertCounts = shape<AlertsResponse["counts"]>("AlertCounts", { open: value, unacknowledged: value });

const alert = shape<AlertJSON>("Alert", {
  id: value,
  kind: value,
  severity: value,
  status: value,
  muted: value,
  object: shape("AlertObject", { kind: value, id: value, name: value }),
  machine_id: value,
  machine_name: value,
  service_id: value,
  details: shape("AlertDetails", {
    count: optional(value),
    percent: optional(value),
    mount_point: optional(value),
    threshold: optional(value),
    exit_code: optional(value),
    reason: optional(value),
    target: optional(value),
    not_after: optional(value),
    since: optional(value),
  }),
  opened_at: value,
  updated_at: value,
  resolved_at: value,
  acknowledged_at: value,
});

const alerts = shape<AlertsResponse>("AlertsResponse", { alerts: alert, counts: alertCounts });

const alertResponse = shape<AlertResponse>("AlertResponse", {
  alert,
  deliveries: shape("Delivery", {
    id: value,
    channel_id: value,
    event: value,
    status: value,
    attempts: value,
    reason: value,
    code: value,
    updated_at: value,
    next_attempt_at: value,
  }),
});

const channels = shape<ChannelsResponse>("ChannelsResponse", {
  channels: shape("Channel", {
    id: value,
    name: value,
    url: value,
    format: value,
    destination: optional(value),
    has_secret: value,
    min_severity: value,
    notify_resolve: value,
    receives_audit: value,
    enabled: value,
    plain: value,
    created_at: value,
  }),
});

const mutes = shape<MutesResponse>("MutesResponse", {
  mutes: shape("Mute", {
    id: value,
    kind: value,
    target: shape("Mute.target", { kind: value, id: value, name: value }),
    reason: value,
    starts_at: value,
    ends_at: value,
    active: value,
    created_by_kind: value,
    created_by_name: value,
    created_at: value,
    announce: value,
    public_title: value,
  }),
});

const counts = shape<Counts>("Counts", {
  machines: shape("Counts.machines", { total: value, online: value }),
  jobs: shape("Counts.jobs", { total: value, attention: value }),
  containers: shape("Counts.containers", { total: value, attention: value, updates: value }),
  probes: shape("Counts.probes", { total: value, attention: value }),
  certificates: shape("CertificateCounts", { total: value, expiring: value, expired: value, soonest_expires_at: value }),
  status: shape("Counts.status", { open_incidents: value }),
  alerts: alertCounts,
});

type DeclaredServiceJSON = DeclaredServicesResponse["services"][number];

const serviceUnit = shape<DeclaredServiceJSON["units"][number]>("ServiceUnit", {
  id: value,
  name: value,
  kind: value,
  image: value,
  version: value,
  digest: value,
  container_name: value,
  systemd_unit: value,
  ports: value,
  update_policy: value,
});

const declaredServiceFields = {
  id: value,
  machine_id: value,
  machine_name: value,
  name: value,
  title: value,
  nature: value,
  origin: value,
  compose_project: value,
  public_url: value,
  declared_by: value,
  provides: value,
  units: serviceUnit,
  project_ids: value,
  created_at: value,
  updated_at: value,
} as const;

const declaredServices = shape<DeclaredServicesResponse>("DeclaredServicesResponse", {
  services: shape<DeclaredServiceJSON>("DeclaredService", declaredServiceFields),
});

const linked = shape<DeclaredServiceDetail["links"]["probes"][number]>("LinkedObject", { id: value, name: value });

const declaredService = shape<DeclaredServiceDetail>("DeclaredServiceDetail", {
  ...declaredServiceFields,
  links: shape("ServiceLinks", { probes: linked, components: linked }),
});

const projects = shape<ProjectsResponse>("ProjectsResponse", {
  projects: shape("Project", { id: value, name: value, title: value, service_ids: value, created_at: value, updated_at: value }),
});

type ServiceGapJSON = GapsResponse["services"][number];
type UnitGapJSON = ServiceGapJSON["units"][number];
type UndeclaredGroupJSON = GapsResponse["undeclared"][number];

const gaps = shape<GapsResponse>("GapsResponse", {
  services: shape<ServiceGapJSON>("ServiceGap", {
    service_id: value,
    state: value,
    weak: value,
    units: shape<UnitGapJSON>("UnitGap", {
      unit_id: value,
      name: value,
      kind: value,
      state: value,
      weak: value,
      containers: shape("ContainerVerdict", {
        id: value,
        name: value,
        state: value,
        image: value,
        match_level: value,
        verdict: value,
        reason: value,
      }),
    }),
  }),
  undeclared: shape<UndeclaredGroupJSON>("UndeclaredGroup", {
    machine_id: value,
    machine_name: value,
    key_kind: value,
    key_value: value,
    ignored: value,
    service_id: value,
    adoptable: value,
    containers: shape("UndeclaredContainer", {
      id: value,
      name: value,
      state: value,
      image: value,
      compose_service: value,
      label_service: value,
      label_unit: value,
      label_problem: value,
    }),
  }),
});

const proposal = shape<ProposalResponse>("ProposalResponse", {
  service_id: value,
  definition: shape("ProposalResponse.definition", {
    machine_id: value,
    name: value,
    title: value,
    nature: value,
    origin: value,
    compose_project: value,
    public_url: value,
    provides: value,
    project_ids: value,
    units: shape("ProposedUnit", {
      id: value,
      name: value,
      image: value,
      version: value,
      digest: value,
      container_name: value,
      systemd_unit: value,
      ports: value,
      update_policy: optional(value),
    }),
  }),
});

type ManifestChangeJSON = NonNullable<Apply["changes"]>[number];

const manifestChange = shape<ManifestChangeJSON>("ManifestChange", {
  action: value,
  kind: value,
  id: value,
  name: value,
  fields: shape("ManifestFieldChange", { field: value, before: value, after: value }),
  notes: value,
  carries: shape("ManifestCarry", { what: value, count: value }),
});

const apply = shape<Apply>("Apply", {
  id: value,
  at: value,
  finished_at: value,
  actor_kind: value,
  actor_name: value,
  file_hash: value,
  file_name: value,
  prune: value,
  plan_hash: value,
  state: value,
  summary: shape("Apply.summary", { create: value, update: value, delete: value, unchanged: value }),
  done: value,
  error: value,
  error_object: value,
  changes: optional(manifestChange),
});

const mcp = shape<MCPState>("MCPState", {
  enabled: value,
  client_id: value,
  secret_masked: value,
  redirect_uris: value,
  endpoint: value,
  issuer: value,
  url_local: value,
  stdio_command: value,
  server_name: value,
  tokens: shape("MCPToken", { id: value, name: value, masked: value, scope: value, created_at: value, expires_at: value, last_used_at: value }),
  sessions: shape("MCPSession", { id: value, scope: value, started_at: value, last_used_at: value, expires_at: value }),
  limits: shape("MCPState.limits", { tokens: value, redirect_uris: value }),
  lifetimes: shape("MCPState.lifetimes", { access_seconds: value, refresh_seconds: value, api_token_days: value, api_token_default_days: value }),
  tools: shape("MCPState.tools", { reads: value }),
  enabled_at: value,
});

const auth = shape<AuthState>("AuthState", {
  mode: value,
  username: value,
  header: optional(value),
  enabled_at: value,
  password_min_length: value,
  forward_insecure: optional(value),
});

const session = shape<Session>("Session", {
  version: value,
  language: value,
  languages: value,
  certificate_thresholds: shape("CertificateThresholds", { warning: value, danger: value }),
  disk_thresholds: shape("DiskThresholds", { attention: value, danger: value }),
});

type IncidentUpdateJSON = Incident["updates"][number];

const incidentUpdate = shape<IncidentUpdateJSON>("IncidentUpdate", { status: value, message: value, created_at: value });

const statusComponent = shape<StatusComponent>("StatusComponent", {
  id: value,
  page_id: value,
  name: value,
  position: value,
  members: shape("StatusMember", { kind: value, id: value, name: value, state: value, counts: value, present: value }),
  derived: value,
  effective: value,
  created_at: value,
});

const incident = shape<Incident>("Incident", {
  id: value,
  title: value,
  impact: value,
  status: value,
  starts_at: value,
  ends_at: value,
  targets: shape("Incident.targets", { kind: value, id: value, name: value }),
  components: shape("Incident.components", { id: value, name: value }),
  updates: incidentUpdate,
  created_at: value,
  updated_at: value,
  resolved_at: value,
});

type PublicIncidentJSON = PublicStatus["open"][number];

const publicIncident = shape<PublicIncidentJSON>("PublicIncident", {
  id: value,
  title: value,
  impact: value,
  status: value,
  starts_at: value,
  ends_at: value,
  components: value,
  updates: incidentUpdate,
  created_at: value,
  updated_at: value,
  resolved_at: value,
});

const publicStatus = shape<PublicStatus>("PublicStatus", {
  title: value,
  announcement: value,
  language: value,
  generated_at: value,
  global: value,
  components: shape("PublicComponent", {
    id: value,
    name: value,
    state: value,
    days: shape("StatusDay", { day: value, total: value, success: value, degraded: value }),
  }),
  open: publicIncident,
  history: publicIncident,
});

const audit = shape<AuditResponse>("AuditResponse", {
  events: shape("AuditEvent", {
    id: value,
    at: value,
    actor_kind: value,
    actor_name: value,
    action: value,
    object_kind: value,
    object_id: value,
    object_name: value,
    details: dictionaryOf(value),
    apply_id: value,
  }),
  next_before: value,
});

const auditForwarding = shape<AuditForwarding>("AuditForwarding", { channels: value, failing: value });

const actionRun = shape<ActionRun>("ActionRun", {
  id: value,
  kind: value,
  params: dictionaryOf(value),
  machine_id: value,
  machine_name: value,
  agent_id: value,
  state: value,
  reason: value,
  seq: value,
  requested_by: value,
  requested_at: value,
  not_after: value,
  finished_at: value,
  output: shape<RunLine>("RunLine", { at: value, stream: value, line: value }),
});

const actions = shape<ActionsResponse>("ActionsResponse", { runs: actionRun });

const backups = shape<Backups>("Backups", {
  keyring: shape<BackupKeyring>("BackupKeyring", { recipient: value, fingerprint: value, created_at: value }),
  destinations: shape<BackupDestination>("BackupDestination", {
    id: value,
    name: value,
    machine_id: value,
    machine_name: value,
    path: value,
    right: value,
    created_at: value,
  }),
  plan: shape<BackupPlan>("BackupPlan", {
    destination_id: value,
    keep: value,
    schedule: value,
    last_attempt_at: value,
    last_error: value,
    last_run: actionRun,
  }),
});

const deletionCount = shape<DeletionCount>("DeletionCount", { kind: value, count: value });

const deletionPlan = shape<DeletionPlan>("DeletionPlan", {
  blocked: value,
  blockers: shape("DeletionBlocker", { kind: value, objects: shape("DeletionObject", { id: value, name: value }) }),
  removes: deletionCount,
  detaches: deletionCount,
});

// Chaque golden JSON de l'API, et le type du front qui le lit.
const contract: Record<string, Shape<unknown>> = {
  "actions.golden.json": actions,
  "alert.golden.json": alertResponse,
  "alert_channels.golden.json": channels,
  "alert_mutes.golden.json": mutes,
  "alerts.golden.json": alerts,
  "audit.golden.json": audit,
  "audit_forwarding.golden.json": auditForwarding,
  "backups.golden.json": backups,
  "backups_empty.golden.json": backups,
  "auth.golden.json": auth,
  "container.golden.json": containerResponse,
  "containers.golden.json": containers,
  "counts.golden.json": counts,
  "declared_service.golden.json": declaredService,
  "declared_services.golden.json": declaredServices,
  "directory_gaps.golden.json": gaps,
  "directory_proposal.golden.json": proposal,
  "history.golden.json": history,
  "history_hourly.golden.json": history,
  "job.golden.json": jobResponse,
  "jobs.golden.json": jobs,
  "local.golden.json": machine,
  "local_containers.golden.json": containers,
  "local_network.golden.json": network,
  "local_resources.golden.json": current,
  "machine.golden.json": machine,
  "machine_delete_plan.golden.json": deletionPlan,
  "machine_containers.golden.json": containers,
  "machine_network.golden.json": network,
  "machine_probes.golden.json": probes,
  "machines.golden.json": machines,
  "manifest_applies.golden.json": shape<AppliesResponse>("AppliesResponse", { applies: apply }),
  "manifest_apply.golden.json": apply,
  "mcp.golden.json": mcp,
  "network.golden.json": shape<OverviewNetworkResponse>("OverviewNetworkResponse", { machines: network }),
  "probe.golden.json": probeResponse,
  "probes.golden.json": probes,
  "projects.golden.json": projects,
  "remote_resources.golden.json": current,
  "resources.golden.json": shape<ResourcesResponse>("ResourcesResponse", { machines: current }),
  "session.golden.json": session,
  "status_component.golden.json": statusComponent,
  "status_components.golden.json": shape<StatusComponentsResponse>("StatusComponentsResponse", {
    public_url: value,
    global: value,
    components: statusComponent,
  }),
  "status_incident.golden.json": incident,
  "status_incidents.golden.json": shape<IncidentsResponse>("IncidentsResponse", { incidents: incident }),
  "status_pages.golden.json": shape<StatusPagesResponse>("StatusPagesResponse", {
    pages: shape("StatusPage", {
      id: value,
      project_id: value,
      slug: value,
      title: value,
      announcement: value,
      language: value,
      public_url: value,
      created_at: value,
      updated_at: value,
    }),
  }),
  "status_public.golden.json": publicStatus,
};

// Les réponses des outils MCP : un agent IA les lit, pas le navigateur.
const mcpToolGolden = "mcp_";

// Les autres goldens JSON que le front ne lit pas, chacun avec sa raison.
const notReadByFront: Record<string, string> = {
  "manifest_plan.golden.json": "le plan se lit par la commande apply ; l'écran ne montre que les passages",
  "transitions.golden.json": "la fiche d'un conteneur reçoit ses transitions avec lui ; aucun module ne lit la route seule",
};

function isNotReadByFront(name: string): boolean {
  return name.startsWith(mcpToolGolden) || name in notReadByFront;
}

function isOptional(field: Field): field is Optional {
  return typeof field === "object" && "optional" in field;
}

// compareKeys rend les écarts entre une valeur du golden et sa description,
// en descendant dans les objets, les listes et les dictionnaires.
function compareKeys(found: unknown, spec: Spec, path: string, gaps: string[]): void {
  if (spec === "value" || found === null || found === undefined) {
    return;
  }
  if (Array.isArray(found)) {
    found.forEach((item, index) => compareKeys(item, spec, `${path}[${index}]`, gaps));
    return;
  }
  if (typeof found !== "object") {
    gaps.push(`${path} : un objet attendu, le golden porte ${JSON.stringify(found)}`);
    return;
  }
  const record = found as Record<string, unknown>;
  if ("dictionaryOf" in spec) {
    for (const [key, item] of Object.entries(record)) {
      compareKeys(item, spec.dictionaryOf, `${path}.${key}`, gaps);
    }
    return;
  }
  for (const key of Object.keys(record)) {
    if (!(key in spec.fields)) {
      gaps.push(`${path} : clé \`${key}\` absente du type \`${spec.name}\``);
    }
  }
  for (const [key, field] of Object.entries(spec.fields)) {
    if (!(key in record)) {
      if (!isOptional(field)) {
        gaps.push(`${path} : clé \`${key}\` du type \`${spec.name}\` absente du golden`);
      }
      continue;
    }
    compareKeys(record[key], isOptional(field) ? field.optional : field, `${path}.${key}`, gaps);
  }
}

// Les goldens sont lus au chargement du test, hors de web/ : Vite les
// résout depuis ce fichier.
const goldens = import.meta.glob<unknown>("../../../internal/server/testdata/*.golden.json", { eager: true, import: "default" });

function goldenName(path: string): string {
  return path.slice(path.lastIndexOf("/") + 1);
}

test("les goldens du serveur sont lus", () => {
  expect(Object.keys(goldens).length).toBeGreaterThan(0);
});

test("chaque golden JSON a son type, ou la raison pour laquelle le front ne le lit pas", () => {
  const names = Object.keys(goldens).map(goldenName);
  const unknown = names.filter((name) => !(name in contract) && !isNotReadByFront(name));
  expect(unknown).toEqual([]);
  const missing = Object.keys(contract).filter((name) => !names.includes(name));
  expect(missing).toEqual([]);
});

const described = Object.entries(goldens)
  .map(([path, golden]) => [goldenName(path), golden] as const)
  .filter(([name]) => name in contract);

test.each(described)("%s a les clés de son type", (name, golden) => {
  const description = contract[name];
  if (description === undefined) {
    throw new Error(`${name} sans type`);
  }
  const gaps: string[] = [];
  compareKeys(golden, description, name, gaps);
  expect(gaps).toEqual([]);
});

test("compareKeys nomme la clé en trop et la clé absente", () => {
  const described = shape<{ id: string; note?: string; tags: { name: string }[] }>("Exemple", {
    id: value,
    note: optional(value),
    tags: shape("Tag", { name: value }),
  });
  const gaps: string[] = [];
  compareKeys({ id: "a", extra: 1, tags: [{ name: "x" }, {}] }, described, "g", gaps);
  expect(gaps).toEqual(["g : clé `extra` absente du type `Exemple`", "g.tags[1] : clé `name` du type `Tag` absente du golden"]);
});
