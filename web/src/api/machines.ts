import { useResource, type Resource } from "../hooks/useResource";
import { api, segment } from "./client";
import { route } from "./routes";
import type { DeletionPlan } from "./deletion";
import type { Lifecycle } from "./lifecycle";
import type { ProxmoxSummary } from "./proxmox";
import type { ModuleID } from "../lib/modules";

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
  // Ce que l'opérateur en fait, choisi à l'ajout ; et les modules qu'elle porte,
  // ceux de son rôle et ceux que l'agent observe.
  role: RoleID;
  modules: ModuleID[];
  // Où elle tourne : le type retenu, la correction de l'opérateur seule (vide
  // sans), et l'hôte qui la porte, null s'il n'est pas connu.
  kind: KindID;
  kind_override: KindID | "";
  host: MachineHost | null;
}

// Les types de machine (liste fermée du serveur). « vps » se déduit : virtualisée
// sans hôte connu, avec une adresse publique ; sinon c'est une VM.
export type KindID = "physical" | "vm" | "lxc" | "vps" | "unknown";

// L'hôte d'une machine : celui que l'opérateur a désigné (override), sinon
// celui dont l'API de l'hyperviseur rapproche la machine d'un invité (guest).
export interface MachineHost {
  machine_id: string;
  machine_name: string;
  node: string;
  source: "override" | "guest";
}

// Les rôles qu'une machine peut recevoir (liste fermée du serveur).
export type RoleID = "generic" | "docker" | "proxmox";

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

// Une étape de l'installation de l'agent : son identifiant (le titre vient du
// catalogue) et le bloc à coller tel quel sur la machine.
export interface InstallStep {
  id: "stop" | "binary" | "prepare" | "enroll" | "unit" | "rights";
  command: string;
}

export interface TokenResponse {
  name: string;
  // Le jeton n'est montré qu'une fois, dans la commande de l'étape « enroll ».
  token: string;
  steps: InstallStep[];
  // Les commandes à jouer en root sur l'hôte Proxmox, une par ligne, pour créer
  // le jeton local des droits proxmox.* ; vide sans l'un de ces droits.
  proxmox_commands: string;
  expires_at: string;
  url_local: boolean;
}

const machinesPath = "/api/machines";

function machinePath(id: string): string {
  return `${machinesPath}/${segment(id)}`;
}

// wanted à faux : rien n'est lu, pour une liste qui ne sert que sous un choix.
export function useMachines(wanted = true): Resource<MachinesResponse> {
  return useResource<MachinesResponse>(wanted ? machinesPath : null);
}

// Un jeton à usage unique pour enrôler une machine neuve, avec les droits
// cochés ; observe s'y ajoute toujours.
export async function createMachineToken(name: string, role: RoleID, capabilities: Capability[]): Promise<TokenResponse> {
  return api.send<TokenResponse>(route("POST /api/machines/tokens"), { name, role, capabilities });
}

// Change le rôle de la machine, donc ses modules ; ses droits ne bougent pas.
export async function setMachineRole(machineID: string, role: RoleID): Promise<void> {
  await api.send(route("PUT /api/machines/{id}/role", { id: machineID }), { role });
}

// Corrige où tourne la machine : son type (vide : le type déduit) et l'hôte qui
// la porte (vide : aucun désigné).
export async function setMachinePlacement(machineID: string, kindOverride: KindID | "", hostOverride: string): Promise<Machine> {
  return api.send<Machine>(route("PUT /api/machines/{id}/placement", { id: machineID }), { kind_override: kindOverride, host_override: hostOverride });
}

export async function deleteMachineToken(id: string): Promise<void> {
  return api.send(route("DELETE /api/machines/tokens/{id}", { id }));
}

// La ligne d'installation de l'agent de la machine openCloud, tant qu'elle
// n'en a jamais eu : l'écran de première configuration la montre.
export async function offerLocalEnrollment(): Promise<TokenResponse> {
  return api.send<TokenResponse>(route("POST /api/machines/local-enrollment"));
}

// Un nouveau jeton pour le même identifiant de machine, avec les droits de son
// nouvel agent : c'est ainsi qu'on change ceux d'un agent.
export async function reenrollMachine(id: string, capabilities: Capability[]): Promise<TokenResponse> {
  return api.send<TokenResponse>(route("POST /api/machines/{id}/actions/reenroll", { id }), { capabilities });
}

// Supprimer la machine : effacée avec son historique, ou, avec keepHistory,
// archivée (l'agent est refusé et s'arrête, rien n'est effacé).
export async function deleteMachine(id: string, keepHistory: boolean): Promise<void> {
  return api.send(route("DELETE /api/machines/{id}", { id }), { keep_history: keepHistory });
}

// Ce que le retrait ferait, lu avant de le confirmer.
export async function fetchMachineDeletePlan(id: string): Promise<DeletionPlan> {
  return api.get<DeletionPlan>(`${machinePath(id)}/delete-plan`);
}

// La machine vérifie ses images à son rythme ; la réponse dit seulement que
// la demande est partie.
export async function checkMachineUpdates(id: string): Promise<void> {
  return api.send(route("POST /api/machines/{id}/actions/check-updates", { id }));
}
