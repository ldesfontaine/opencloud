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
  hostname: string;
  address: string;
  os: string;
  arch: string;
  online: boolean;
  last_seen_at: string | null;
  // L'agent courant : l'actif, sinon le dernier révoqué ; null tant
  // qu'aucun ne s'est enrôlé.
  agent: Agent | null;
  // Le support du système, seulement quand la fin de support est activée
  // sous Paramètres.
  lifecycle?: Lifecycle;
}

// Un droit de l'agent et, pour un droit borné, ses chemins.
export interface Capability {
  name: string;
  paths: string[];
}

// Ce que l'agent rapporte de son fichier, ce que son jeton demandait, et
// s'ils diffèrent. Aucun droit n'est encore appliqué.
export interface Agent {
  id: string;
  version: string;
  enrolled_at: string;
  revoked_at: string | null;
  capabilities: Capability[];
  reported_at: string | null;
  requested: Capability[];
  capabilities_differ: boolean;
  // L'empreinte de la clé du serveur que l'agent a épinglée ; vide tant
  // qu'il n'a rien dit, ou s'il n'en a pas.
  server_key_fingerprint: string;
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

// Un jeton à usage unique pour enrôler une machine neuve, avec les droits
// cochés ; observe s'y ajoute toujours.
export async function createMachineToken(name: string, capabilities: Capability[]): Promise<TokenResponse> {
  return api.post<TokenResponse>(tokensPath, { name, capabilities });
}

export async function deleteMachineToken(id: string): Promise<void> {
  return api.delete(`${tokensPath}/${segment(id)}`);
}

// La ligne d'installation de l'agent de la machine openCloud, tant qu'elle
// n'en a jamais eu : l'écran de première configuration la montre.
export async function offerLocalEnrollment(): Promise<TokenResponse> {
  return api.post<TokenResponse>(`${machinesPath}/local-enrollment`);
}

// Un nouveau jeton pour le même identifiant de machine.
export async function reenrollMachine(id: string): Promise<TokenResponse> {
  return api.post<TokenResponse>(`${machinePath(id)}/actions/reenroll`);
}

// L'agent est refusé et s'arrête ; la machine et son historique restent.
export async function revokeAgent(id: string): Promise<void> {
  return api.post(`${machinePath(id)}/actions/revoke`);
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
