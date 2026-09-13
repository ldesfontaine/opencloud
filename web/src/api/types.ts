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
