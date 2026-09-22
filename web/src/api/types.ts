// Ce que l'API rend, tel quel : des faits, des instants ISO en UTC, des
// durées en secondes ou en millisecondes. Le formatage vit dans le front.

export interface Session {
  version: string;
  language: string;
  languages: string[];
  // Les jours à partir desquels un certificat se signale. Le serveur les
  // tient : le compteur et la couleur d'une ligne basculent sur le même
  // chiffre.
  certificate_thresholds: CertificateThresholds;
  // Les pourcentages à partir desquels un volume alerte, puis s'aggrave.
  disk_thresholds: DiskThresholds;
}

export interface CertificateThresholds {
  warning: number;
  danger: number;
}

export interface DiskThresholds {
  attention: number;
  danger: number;
}

export interface Counts {
  machines: { total: number; online: number };
  jobs: { total: number; attention: number };
  // Les services qui vont mal, et ceux qui ont une image plus récente.
  services: { total: number; attention: number; updates: number };
  probes: { total: number; attention: number };
  certificates: CertificateCounts;
  // Les incidents ouverts sur la page de statut.
  status: { open_incidents: number };
  // Les alertes ouvertes, et celles que personne n'a acquittées.
  alerts: AlertCounts;
}

export interface AlertCounts {
  open: number;
  unacknowledged: number;
}

// Les certificats vus par les sondes actives. expiring et expired sont
// disjoints ; soonest_expires_at est la plus proche échéance à venir, nulle
// quand il n'y en a aucune.
export interface CertificateCounts {
  total: number;
  expiring: number;
  expired: number;
  soonest_expires_at: string | null;
}

export interface Machine {
  id: string;
  name: string;
  kind: "local" | "remote";
  hostname: string;
  address: string;
  os: string;
  arch: string;
  agent_version: string;
  local: boolean;
  online: boolean;
  enrolled_at: string;
  last_seen_at: string | null;
}

export interface PendingToken {
  id: string;
  name: string;
  masked: string;
  expires_at: string;
  reenroll: boolean;
}

export interface MachinesResponse {
  machines: Machine[];
  pending: PendingToken[];
}

export interface TokenResponse {
  name: string;
  token: string;
  command: string;
  expires_at: string;
  url_local: boolean;
}

export type JobStatus = "new" | "on_time" | "started" | "late" | "failed" | "paused";

export interface Job {
  id: string;
  name: string;
  machine_id: string;
  machine_name: string;
  status: JobStatus;
  interval_seconds: number;
  grace_seconds: number;
  last_ping_at: string | null;
  next_deadline_at: string | null;
  run_started_at: string | null;
  last_exit_code: number | null;
  last_duration_ms: number | null;
  created_at: string;
}

export type RunOutcome = "in_progress" | "success" | "failure" | "timeout";

export interface Run {
  id: number;
  started_at: string | null;
  completed_at: string | null;
  duration_ms: number | null;
  exit_code: number | null;
  outcome: RunOutcome;
  payload: string;
}

export interface Ping {
  id: number;
  kind: "finish" | "start" | "exit_code";
  exit_code: number | null;
  source: string;
  method: string;
  received_at: string;
}

export interface Snippet {
  key: string;
  code: string;
}

export interface JobResponse {
  job: Job;
  ping_url: string;
  url_local: boolean;
  snippets: Snippet[];
  runs: Run[];
  pings: Ping[];
}

export interface JobsResponse {
  jobs: Job[];
}

// Un volume réel de la machine, nommé par son point de montage.
export interface Disk {
  mount_point: string;
  device: string;
  used: number;
  total: number;
}

// Une lecture de l'agent : instant, pourcentage processeur, octets et
// octets par seconde ; les pourcentages de mémoire et de disque se calculent
// ici. disk_used et disk_total font la somme des volumes. Un point
// d'historique a la même forme, daté du début de son seau, sans les volumes.
export interface Reading {
  sampled_at: string;
  cpu_percent: number;
  cpu_cores: number;
  load_1: number;
  mem_used: number;
  mem_total: number;
  swap_used: number;
  swap_total: number;
  disk_used: number;
  disk_total: number;
  net_rx_per_second: number;
  net_tx_per_second: number;
  disks?: Disk[];
}

// La valeur courante d'une machine : la dernière lecture, disponible si
// elle a moins de 90 s ; sans lecture, null, jamais des zéros.
export interface Current {
  machine_id: string;
  available: boolean;
  sample: Reading | null;
}

export interface ResourcesResponse {
  machines: Current[];
}

export type WindowName = "1h" | "24h" | "7d" | "30d" | "90d";

export interface HistoryResponse {
  machine_id: string;
  window: WindowName;
  span_seconds: number;
  step_seconds: number;
  points: Reading[];
}

// Un port publié sur l'hôte d'une machine.
export interface Port {
  ip: string;
  host_port: number;
  container_port: number;
  protocol: string;
}

// La présence d'un service sur un réseau de sa machine ; l'adresse est
// vide quand le conteneur est arrêté.
export interface Attachment {
  network_id: string;
  name: string;
  ip: string;
  aliases: string[];
}

// Une dépendance déclarée : par le label Compose ou par un lien hérité.
export interface Dependency {
  name: string;
  source: "compose" | "link";
}

export type FindingKind = "host_network" | "privileged" | "database_port_public" | "port_public";

// Un constat d'exposition, calculé par le serveur à la lecture : « warn »
// fait un point orange sur le nœud, « info » une ligne dans l'inspecteur.
export interface Finding {
  kind: FindingKind;
  level: "info" | "warn";
  port?: number;
  protocol?: string;
}

export type ServiceState = "created" | "running" | "paused" | "restarting" | "removing" | "exited" | "dead";
export type ServiceHealth = "" | "starting" | "healthy" | "unhealthy";

// La mesure courante d'un service, absente si elle n'est pas fraîche.
export interface ServiceSample {
  sampled_at: string;
  cpu_percent: number;
  mem_used: number;
  mem_limit: number;
}

// Un service : des faits ; la pastille se dérive ici de l'état, du code
// de sortie et de la santé.
export interface Service {
  id: string;
  machine_id: string;
  machine_name: string;
  kind: "container";
  name: string;
  group: string;
  container_id: string;
  image: string;
  image_id: string;
  // Ce que Compose dit du service : de quoi lire la commande fabriquée.
  compose_service: string;
  compose_dir: string;
  compose_file: string;
  update_policy: UpdatePolicy;
  // Le dernier constat de l'agent sur l'image ; nul tant qu'il n'a rien dit.
  image_check: ImageCheck | null;
  state: ServiceState;
  exit_code: number;
  health: ServiceHealth;
  restart_count: number;
  ports: Port[];
  // bridge, host, none, container:<id>, ou le nom du premier réseau joint.
  network_mode: string;
  privileged: boolean;
  networks: Attachment[];
  depends_on: Dependency[];
  exposure: Finding[];
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
  first_seen_at: string;
  last_seen_at: string;
  archived_at: string | null;
  current: ServiceSample | null;
}

// Ce que l'opérateur veut des mises à jour d'un service : les suivre,
// les taire (épinglé), ne jamais interroger le registre (exclu).
export type UpdatePolicy = "" | "pinned" | "excluded";

export type UpdateKind = "" | "major" | "minor" | "patch" | "digest";

export type CheckOutcome = "ok" | "local" | "unreachable" | "unauthorized" | "not_found" | "unsupported";

// Le constat de l'agent sur une image, et ce que le serveur en déduit :
// le type, l'image à tirer, la commande à copier, jamais exécutée.
export interface ImageCheck {
  checked_at: string;
  outcome: CheckOutcome;
  local_digest: string;
  remote_digest: string;
  newer_tag: string;
  newer_digest: string;
  kind: UpdateKind;
  target: string;
  command: string;
}

// Ce qu'une machine dit de son Docker.
export interface Engine {
  machine_id: string;
  present: boolean;
  reason: "" | "no_socket" | "denied" | "down" | "too_old";
  version: string;
  api_version: string;
  checked_at: string;
}

export interface ServicesResponse {
  services: Service[];
  engines: Engine[];
}

export interface Transition {
  id: number;
  at: string;
  action: string;
  previous_state: ServiceState | "";
  new_state: ServiceState | "";
  previous_health: ServiceHealth;
  new_health: ServiceHealth;
  exit_code: number | null;
  replayed: boolean;
  snippet: string;
}

export interface ServiceResponse {
  service: Service;
  engine: Engine | null;
  transitions: Transition[];
}

// Un groupe du graphe : un réseau créé par l'opérateur ou par Compose, et
// les services que le serveur y a placés.
export interface Group {
  network_id: string;
  name: string;
  internal: boolean;
  members: string[];
}

// Une arête : d'Internet à un port publié, ou d'un service à celui dont il
// dépend. Le nœud Internet a l'identifiant « internet ».
export interface Edge {
  from: string;
  to: string;
  kind: "public" | "depends";
  port?: number;
  protocol?: string;
  source?: "compose" | "link";
}

export interface NetworkResponse {
  machine_id: string;
  machine_name: string;
  online: boolean;
  engine: Engine | null;
  services: Service[];
  groups: Group[];
  edges: Edge[];
}

// Toutes les machines, chacune avec sa topologie, pour la vue d'ensemble.
export interface OverviewNetworkResponse {
  machines: NetworkResponse[];
}

export interface LogLine {
  at: string | null;
  stream: string;
  text: string;
}

export interface LogsResponse {
  lines: LogLine[];
}

export type ProbeStatus = "new" | "up" | "degraded" | "down" | "paused";
export type ProbeOutcome = "up" | "degraded" | "down";
export type ProbeKind = "http" | "tcp";

// Ce que la sonde a vu de la chaîne présentée au dernier essai HTTPS ; la
// fonctionnalité certificats la reprendra.
// La chaîne présentée, en faits. L'échéance et la confiance sont deux
// choses : un certificat d'autorité interne a une date parfaitement
// lisible. L'état et la pastille se dérivent ici, dans lib/certificates.
export interface Certificate {
  subject: string;
  issuer: string;
  not_before: string;
  not_after: string;
  fingerprint: string;
  chain_valid: boolean;
  hostname_match: boolean;
  // Vide quand la cible n'agrafe rien : personne n'est contacté pour le
  // savoir.
  ocsp: "" | "good" | "revoked" | "unknown";
}

// Une sonde : des faits. La pastille, la disponibilité et les libellés se
// dérivent ici. machine_online dit si quelqu'un sonde encore.
export interface Probe {
  id: string;
  name: string;
  kind: ProbeKind;
  target: string;
  machine_id: string;
  machine_name: string;
  machine_online: boolean;
  service_id: string;
  service_name: string;
  status: ProbeStatus;
  interval_seconds: number;
  timeout_seconds: number;
  failure_threshold: number;
  recovery_threshold: number;
  method: string;
  expected_status: string;
  expected_body: string;
  follow_redirects: boolean;
  // Une sonde TCP qui fait une poignée de main plutôt qu'une simple
  // connexion, pour lire le certificat d'un port qui ne parle pas HTTP.
  tls: boolean;
  last_checked_at: string | null;
  last_duration_ms: number;
  last_code: number | null;
  last_reason: string;
  certificate: Certificate | null;
  created_at: string;
}

// Un jour agrégé, en heure UTC : des comptes, jamais un pourcentage.
export interface ProbeDay {
  day: string;
  total: number;
  success: number;
  degraded: number;
  duration_ms: number;
}

export interface ProbeResult {
  checked_at: string;
  outcome: ProbeOutcome;
  duration_ms: number;
  code: number | null;
  reason: string;
}

// Une fenêtre de disponibilité : des essais et des succès ; le pourcentage
// se calcule ici, et une fenêtre sans essai n'en a pas.
export interface ProbeUptime {
  window: string;
  total: number;
  success: number;
}

export interface ProbesResponse {
  probes: Probe[];
  days: Record<string, ProbeDay[]>;
}

export interface ProbeResponse {
  probe: Probe;
  uptime: ProbeUptime[];
  days: ProbeDay[];
  results: ProbeResult[];
}

// La page de statut. Un état est un mot d'une liste fermée ; vide quand
// rien ne compte. La pastille se dérive dans lib/status.
export type StatusState = "operational" | "degraded" | "down" | "maintenance" | "";
export type Impact = "degraded" | "down" | "maintenance";
export type IncidentStatus = "scheduled" | "investigating" | "identified" | "monitoring" | "in_progress" | "resolved";
export type MemberKind = "machine" | "service" | "heartbeat" | "probe";

// Un jour d'uptime d'un composant : la somme des jours de ses sondes.
export interface StatusDay {
  day: string;
  total: number;
  success: number;
  degraded: number;
}

export interface IncidentUpdate {
  status: IncidentStatus;
  message: string;
  created_at: string;
}

// Un objet rattaché, tel que l'opérateur le voit : jamais servi au public.
export interface StatusMember {
  kind: MemberKind;
  id: string;
  name: string;
  state: StatusState;
  counts: boolean;
  present: boolean;
}

export interface StatusComponent {
  id: string;
  name: string;
  position: number;
  members: StatusMember[];
  derived: StatusState;
  effective: StatusState;
  created_at: string;
}

export interface StatusComponentsResponse {
  public_url: string;
  global: StatusState;
  components: StatusComponent[];
}

export interface Incident {
  id: string;
  title: string;
  impact: Impact;
  status: IncidentStatus;
  starts_at: string | null;
  ends_at: string | null;
  components: { id: string; name: string }[];
  updates: IncidentUpdate[];
  created_at: string;
  updated_at: string;
  resolved_at: string | null;
}

export interface IncidentsResponse {
  incidents: Incident[];
}

export interface StatusPageSettings {
  title: string;
  announcement: string;
  language: string;
  public_url: string;
}

// Ce que le public lit : les noms des composants, jamais les objets.
export interface PublicComponent {
  id: string;
  name: string;
  state: StatusState;
  days: StatusDay[];
}

export interface PublicIncident {
  id: string;
  title: string;
  impact: Impact;
  status: IncidentStatus;
  starts_at: string | null;
  ends_at: string | null;
  components: string[];
  updates: IncidentUpdate[];
  created_at: string;
  updated_at: string;
  resolved_at: string | null;
}

export interface PublicStatus {
  title: string;
  announcement: string;
  language: string;
  generated_at: string;
  global: StatusState;
  components: PublicComponent[];
  open: PublicIncident[];
  history: PublicIncident[];
}

// --- Alertes ---

export type AlertKind =
  | "machine_lost"
  | "service_down"
  | "service_unhealthy"
  | "service_restart"
  | "job_late"
  | "job_failed"
  | "probe_down"
  | "cert_expiring"
  | "cert_expired"
  | "cert_untrusted"
  | "disk_full";

// Le catalogue fermé, dans l'ordre des écrans.
export const alertKinds: readonly AlertKind[] = [
  "machine_lost",
  "service_down",
  "service_unhealthy",
  "service_restart",
  "job_late",
  "job_failed",
  "probe_down",
  "cert_expiring",
  "cert_expired",
  "cert_untrusted",
  "disk_full",
];

export type AlertSeverity = "attention" | "danger";
export type AlertStatus = "open" | "resolved";
export type AlertObjectKind = "machine" | "service" | "heartbeat" | "probe" | "volume";

export interface AlertObject {
  kind: AlertObjectKind;
  id: string;
  name: string;
}

// Les chiffres du fait ; la phrase se fait ici avec les mêmes clés que le
// canal.
export interface AlertDetails {
  count?: number;
  percent?: number;
  mount_point?: string;
  exit_code?: number;
  reason?: string;
  target?: string;
  not_after?: string;
  since?: string;
}

export interface Alert {
  id: number;
  kind: AlertKind;
  severity: AlertSeverity;
  status: AlertStatus;
  // Ouverte sous un silence ou une maintenance : montrée, jamais envoyée.
  silenced: boolean;
  object: AlertObject;
  machine_id: string;
  machine_name: string;
  details: AlertDetails;
  opened_at: string;
  updated_at: string;
  resolved_at: string | null;
  acknowledged_at: string | null;
}

export interface AlertsResponse {
  alerts: Alert[];
  counts: AlertCounts;
}

export type DeliveryEvent = "opened" | "aggravated" | "resolved";
export type DeliveryStatus = "pending" | "delivered" | "failed";

export interface Delivery {
  id: number;
  channel_id: number;
  event: DeliveryEvent;
  status: DeliveryStatus;
  attempts: number;
  reason: string;
  code: number;
  updated_at: string;
}

export interface AlertResponse {
  alert: Alert;
  deliveries: Delivery[];
}

export type ChannelFormat = "json" | "text" | "discord" | "slack";

export interface Channel {
  id: number;
  name: string;
  url: string;
  format: ChannelFormat;
  has_secret: boolean;
  min_severity: AlertSeverity;
  notify_resolve: boolean;
  enabled: boolean;
  // L'URL est en http : le canal parle en clair.
  plain: boolean;
  created_at: string;
}

export interface ChannelsResponse {
  channels: Channel[];
}

export interface ChannelTest {
  ok: boolean;
  reason: string;
  code: number;
}

export interface Silence {
  id: number;
  kind: AlertKind | "";
  object: { kind: AlertObjectKind | ""; id: string; name: string };
  reason: string;
  starts_at: string;
  ends_at: string;
  active: boolean;
  created_at: string;
}

export interface SilencesResponse {
  silences: Silence[];
}
