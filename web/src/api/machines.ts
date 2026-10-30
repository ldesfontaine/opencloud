import { useResource, type Resource } from "../hooks/useResource";
import { api, segment } from "./client";
import type { DeletionPlan } from "./deletion";
import type { Lifecycle } from "./lifecycle";
import type { ProxmoxSummary } from "./proxmox";

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
  // À mettre à jour en dernier : la machine porte le chemin des ordres
  // (proxy, VPN…). Réglé par l'opérateur seul.
  update_last: boolean;
  last_seen_at: string | null;
  // L'agent courant : l'actif, sinon le dernier révoqué ; null tant
  // qu'aucun ne s'est enrôlé.
  agent: Agent | null;
  // Le support du système, seulement quand la fin de support est activée
  // sous Paramètres.
  lifecycle?: Lifecycle;
  // L'hôte Proxmox que la machine porte, ou la VM qu'elle est sur un hôte.
  proxmox: ProxmoxSummary;
  // Ce que l'agent reconnaît sur la machine par ses signes locaux ; vide sans
  // agent ou quand rien n'est reconnu. Un rôle ne donne aucun droit.
  roles: MachineRole[];
}

// Un rôle de la liste fermée du serveur (« proxmox_ve »), avec la version
// trouvée, vide si elle n'est pas lisible.
export interface MachineRole {
  name: string;
  version: string;
}

// Un droit de l'agent et, pour un droit borné, ses chemins.
export interface Capability {
  name: string;
  paths: string[];
}

// Un droit tel que l'agent le rapporte : unavailable dit pourquoi son compte
// ne peut pas l'exercer sur la machine (vide quand il le peut). Le droit coché
// reste coché : l'agent refusera l'ordre, avec cette raison.
export interface ReportedCapability extends Capability {
  unavailable: string;
}

// Le compte système sous lequel l'agent tourne ; name est vide quand il n'a
// pas de nom lisible, l'uid 0 dit alors root.
export interface Account {
  uid: number;
  name: string;
}

// Ce que l'agent rapporte de son fichier, ce que son jeton demandait, et
// s'ils diffèrent. L'agent applique ceux de son fichier.
export interface Agent {
  id: string;
  version: string;
  enrolled_at: string;
  revoked_at: string | null;
  capabilities: ReportedCapability[];
  reported_at: string | null;
  requested: Capability[];
  capabilities_differ: boolean;
  // L'empreinte de la clé du serveur que l'agent a épinglée ; vide tant
  // qu'il n'a rien dit, ou s'il n'en a pas.
  server_key_fingerprint: string;
  // Le compte que l'agent a dit à l'ouverture de son flux ; null tant qu'il
  // n'a rien dit (un agent plus ancien).
  run_as: Account | null;
  // L'agent revient seul à sa version d'avant si une mise à jour n'est pas
  // confirmée ; sans cela, le serveur ne lui en envoie aucune.
  rollback_capable: boolean;
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
  // Les commandes à jouer une fois, en root, avant `command`.
  preparation: string;
  // Les commandes à jouer en root sur l'hôte Proxmox, une par ligne, pour créer
  // le jeton local des droits proxmox.* ; vide sans l'un de ces droits.
  proxmox_commands: string;
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

// Un nouveau jeton pour le même identifiant de machine. Avec des droits,
// c'est changer ceux de son agent ; sans, il garde ceux d'avant.
export async function reenrollMachine(id: string, capabilities?: Capability[]): Promise<TokenResponse> {
  return api.post<TokenResponse>(`${machinePath(id)}/actions/reenroll`, capabilities === undefined ? {} : { capabilities });
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
