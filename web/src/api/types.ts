// Ce que l'API rend, tel quel : des faits, des instants ISO en UTC, des
// durées en secondes ou en millisecondes. Le formatage vit dans le front.

export interface Session {
  version: string;
  language: string;
  languages: string[];
}

export interface Counts {
  machines: { total: number; online: number };
  jobs: { total: number; attention: number };
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
