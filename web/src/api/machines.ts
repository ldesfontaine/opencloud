import { useResource, type Resource } from "../hooks/useResource";
import { api, segment } from "./client";
import type { DeletionPlan } from "./deletion";
import type { Lifecycle } from "./lifecycle";

// Les machines et leurs jetons d'enrôlement, sur le modèle d'api/probes.
// Sujet du direct : « machines », que topicsOf devine du chemin. Ce qu'une
// machine porte (sondes, conteneurs, mesures, réseau) se lit par le module
// de cette ressource.

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
  // Le support du système, seulement quand la fin de support est activée
  // sous Paramètres.
  lifecycle?: Lifecycle;
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

const machinesPath = "/api/machines";
const tokensPath = `${machinesPath}/tokens`;

function machinePath(id: string): string {
  return `${machinesPath}/${segment(id)}`;
}

// wanted à faux : rien n'est lu, pour une liste qui ne sert que sous un choix.
export function useMachines(wanted = true): Resource<MachinesResponse> {
  return useResource<MachinesResponse>(wanted ? machinesPath : null);
}

// Un jeton à usage unique pour enrôler une machine neuve.
export async function createMachineToken(name: string): Promise<TokenResponse> {
  return api.post<TokenResponse>(tokensPath, { name });
}

export async function deleteMachineToken(id: string): Promise<void> {
  return api.delete(`${tokensPath}/${segment(id)}`);
}

// Un nouveau jeton pour le même identifiant de machine.
export async function reenrollMachine(id: string): Promise<TokenResponse> {
  return api.post<TokenResponse>(`${machinePath(id)}/actions/reenroll`);
}

export async function deleteMachine(id: string): Promise<void> {
  return api.delete(machinePath(id));
}

// Ce que le retrait ferait, lu avant de le confirmer.
export async function fetchMachineDeletePlan(id: string): Promise<DeletionPlan> {
  return api.get<DeletionPlan>(`${machinePath(id)}/delete-plan`);
}

// La machine vérifie ses images à son rythme ; la réponse dit seulement que
// la demande est partie.
export async function checkMachineUpdates(id: string): Promise<void> {
  return api.post(`${machinePath(id)}/actions/check-updates`);
}
