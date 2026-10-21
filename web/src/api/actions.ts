import { useResource, type Resource } from "../hooks/useResource";
import { api, segment } from "./client";

// Les ordres envoyés aux agents (agent.ping pour l'instant) et ce qu'ils
// deviennent. Sujet du direct : « actions », que topicsOf devine du chemin.
// Le genre, l'état et la raison se traduisent sous action.*.

export type ActionState = "queued" | "sent" | "accepted" | "refused" | "succeeded" | "failed" | "expired";

export interface RunLine {
  at: string;
  stream: string;
  line: string;
}

export interface ActionRun {
  id: string;
  // agent.ping : la liste fermée du protocole.
  kind: string;
  // Sans secret ; {} pour un ordre qui n'en prend pas.
  params: Record<string, unknown>;
  // null : la machine ou l'agent a été supprimé depuis, le run reste.
  machine_id: string | null;
  machine_name: string;
  agent_id: string | null;
  state: ActionState;
  // La raison de l'agent, ou « undelivered » ; vide sinon.
  reason: string;
  seq: number;
  requested_by: string;
  requested_at: string;
  not_after: string;
  finished_at: string | null;
  output: RunLine[];
}

export interface ActionsResponse {
  runs: ActionRun[];
}

// La clé qui signe les ordres : ce que chaque agent épingle à l'enrôlement.
export interface ServerKey {
  public_key: string;
  fingerprint: string;
  // La clé précédente, le temps d'une rotation : un agent resté dessus
  // reçoit la neuve à l'ouverture de son flux. null hors rotation.
  previous_fingerprint: string | null;
}

// Les derniers ordres d'une machine, ce que sa fiche montre.
const machineActionsLimit = 10;

export function actionsPath(machineID: string): string {
  return `/api/actions?machine=${segment(machineID)}&limit=${machineActionsLimit}`;
}

export function useMachineActions(machineID: string): Resource<ActionsResponse> {
  return useResource<ActionsResponse>(() => actionsPath(machineID));
}

// Où en est la clé épinglée par rapport à celle du serveur ; « current »
// tant que la clé du serveur n'est pas lue.
export function pinnedKeyState(key: ServerKey | null, pinned: string): "current" | "previous" | "differs" {
  if (key === null || key.fingerprint === pinned) return "current";
  if (key.previous_fingerprint === pinned) return "previous";
  return "differs";
}

export function useServerKey(): Resource<ServerKey> {
  return useResource<ServerKey>("/api/agents/server-key");
}

// L'ordre agent.ping : écrit et journalisé avant de partir ; la réponse de
// l'agent arrive par le direct.
export async function pingAgent(machineID: string): Promise<ActionRun> {
  return api.post<ActionRun>(`/api/machines/${segment(machineID)}/actions/ping`);
}
