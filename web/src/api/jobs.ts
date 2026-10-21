import { useResource, type Resource } from "../hooks/useResource";
import { api, segment } from "./client";
import type { DeletionPlan } from "./deletion";

// Les tâches planifiées, sur le modèle d'api/probes. Sujet du direct :
// « jobs », que topicsOf devine du chemin.

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

interface Snippet {
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

// Le corps de la création comme de l'édition ; lib/jobs le construit.
export interface JobRequest {
  name: string;
  machine_id: string;
  interval_minutes: number;
  grace_minutes: number;
}

const jobsPath = "/api/jobs";

function jobPath(id: string): string {
  return `${jobsPath}/${segment(id)}`;
}

// wanted à faux : rien n'est lu, pour une liste qui ne sert que sous un choix.
export function useJobs(wanted = true): Resource<JobsResponse> {
  return useResource<JobsResponse>(wanted ? jobsPath : null);
}

// id nul : rien à lire (un formulaire de création).
export function useJob(id: string | null): Resource<JobResponse> {
  return useResource<JobResponse>(id === null ? null : () => jobPath(id));
}

// Sans id, une création ; avec, le remplacement de cette tâche.
export async function saveJob(id: string | undefined, body: JobRequest): Promise<Job> {
  return id === undefined ? api.post<Job>(jobsPath, body) : api.put<Job>(jobPath(id), body);
}

export async function deleteJob(id: string): Promise<void> {
  return api.delete(jobPath(id));
}

export async function fetchJobDeletePlan(id: string): Promise<DeletionPlan> {
  return api.get<DeletionPlan>(`${jobPath(id)}/delete-plan`);
}

export async function pauseJob(id: string): Promise<void> {
  return api.post(`${jobPath(id)}/actions/pause`);
}

export async function resumeJob(id: string): Promise<void> {
  return api.post(`${jobPath(id)}/actions/resume`);
}

// L'ancienne URL de ping cesse aussitôt de répondre.
export async function regenerateJobToken(id: string): Promise<void> {
  return api.post(`${jobPath(id)}/actions/regenerate-token`);
}
