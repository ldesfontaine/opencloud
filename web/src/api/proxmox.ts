import { useResource, type Resource } from "../hooks/useResource";
import type { ActionRun } from "./actions";
import { api, segment } from "./client";
import { route } from "./routes";

// L'hôte Proxmox qu'une machine porte et ce que son API en dit. Sujet du
// direct : « machines », que topicsOf devine du chemin. Le secret du jeton
// n'est jamais rendu par le serveur : il ne sort d'ici que dans la requête qui
// déclare l'hôte.

// La VM ou le conteneur : « qemu » ou « lxc ».
export type ProxmoxKind = "qemu" | "lxc";

export interface ProxmoxGuest {
  vmid: number;
  kind: ProxmoxKind;
  name: string;
  // running, stopped, paused… tel que Proxmox le dit.
  status: string;
  template: boolean;
  node: string;
  // Ce qui est alloué, pas ce qui est consommé.
  cpus: number;
  max_memory: number;
  max_disk: number;
  // La machine enrôlée qui porte ce nom d'hôte ; null sans rapprochement.
  linked_machine_id: string | null;
  last_backup_at: string | null;
  last_backup_bytes: number;
  // Le stockage où se trouve la plus récente ; vide sans archive.
  last_backup_storage: string;
  // Vrai quand last_backup_at est null faute de droit de lecture des archives,
  // non faute d'archive : l'écran dit « Inconnue », jamais « Aucune ».
  backup_unknown: boolean;
  // Les instantanés (oc-AAAAMMJJ-HHMMSS) que les ordres de ce produit ont
  // créés et que nul n'a supprimés depuis : les seuls qu'on propose de supprimer.
  own_snapshots: string[];
}

export interface ProxmoxStorage {
  node: string;
  name: string;
  type: string;
  total: number;
  used: number;
  avail: number;
  // Les contenus que le stockage accepte (backup, iso…).
  content: string[];
  active: boolean;
}

// Les commandes pveum qui donnent au jeton la lecture des archives, sur les
// seuls stockages qu'il n'a pas pu lister.
export interface BackupGrant {
  privileges: string[];
  role: string;
  commands: string[];
}

export interface ProxmoxHost {
  api_url: string;
  // Les quatre premiers caractères de l'identifiant : le serveur ne le rend jamais en entier.
  token_id_hint: string;
  // L'empreinte SHA-256 épinglée ; vide : le magasin du système juge.
  pin: string;
  node: string;
  version: string;
  verified_at: string;
  last_read_at: string | null;
  // La clé du refus du dernier relevé qui a échoué ; vide s'il a réussi.
  last_error: string;
  // Les stockages dont le jeton n'a pas pu lister les archives faute de droit.
  backup_blind_storages: string[];
  backup_grant: BackupGrant | null;
  guests: ProxmoxGuest[];
  storages: ProxmoxStorage[];
}

// La VM qu'une machine enrôlée est sur un hôte Proxmox.
interface ProxmoxHosted {
  host_machine_id: string;
  host_machine_name: string;
  // Le nœud de l'hôte qui l'héberge.
  node: string;
  vmid: number;
  kind: ProxmoxKind;
  name: string;
  status: string;
  cpus: number;
  max_memory: number;
  max_disk: number;
  last_backup_at: string | null;
  last_backup_bytes: number;
  backup_unknown: boolean;
}

// La grappe que plusieurs hôtes déclarés partagent.
export interface ProxmoxCluster {
  id: string;
  name: string;
}

// Un invité vu depuis la liste des machines : de quoi le placer dans l'arbre,
// qu'une machine enrôlée le porte ou non.
export interface ProxmoxMember {
  vmid: number;
  kind: ProxmoxKind;
  name: string;
  status: string;
  template: boolean;
  node: string;
  cpus: number;
  max_memory: number;
  max_disk: number;
  linked_machine_id: string | null;
}

// Ce que la liste des machines dit de Proxmox pour chacune.
export interface ProxmoxSummary {
  declared: boolean;
  guests: number;
  last_error: string;
  // Vrai quand le jeton ne peut pas lister les archives d'un stockage.
  backup_blind: boolean;
  // Pour un hôte déclaré : son nœud, sa grappe (null pour un nœud seul) et ses
  // invités. Vides pour une machine sans hôte.
  node: string;
  cluster: ProxmoxCluster | null;
  guest_list: ProxmoxMember[];
  hosted: ProxmoxHosted | null;
}

interface ProxmoxDefinition {
  api_url: string;
  token_id: string;
  token_secret: string;
  pin: string;
}

// La fiche de l'hôte ; `wanted` faux ne lit rien (une machine sans hôte
// déclaré ferait un 404 pour rien).
export function useMachineProxmox(machineID: string, wanted: boolean): Resource<ProxmoxHost> {
  return useResource<ProxmoxHost>(wanted ? () => `/api/machines/${segment(machineID)}/proxmox` : null);
}

export function setMachineProxmox(machineID: string, definition: ProxmoxDefinition): Promise<ProxmoxHost> {
  return api.send<ProxmoxHost>(route("PUT /api/machines/{id}/proxmox", { id: machineID }), definition);
}

// Relire l'hôte tout de suite ; la réponse est la fiche relue (ou, si l'hôte
// refuse, la fiche avec la raison).
export function refreshMachineProxmox(machineID: string): Promise<ProxmoxHost> {
  return api.send<ProxmoxHost>(route("POST /api/machines/{id}/actions/refresh-proxmox", { id: machineID }));
}

export async function removeMachineProxmox(machineID: string): Promise<void> {
  return api.send(route("POST /api/machines/{id}/actions/remove-proxmox", { id: machineID }));
}

// Un geste sur un invité, fait par l'agent de l'hôte : démarrer, arrêter
// proprement, prendre un instantané, supprimer un instantané de ce produit.
export type GuestVerb = "start" | "shutdown" | "snapshot" | "snapshot-delete";

// L'ordre part vers l'agent de la machine qui porte l'hôte ; la réponse est son
// run, que le direct (« actions ») fait avancer. snapshot ne sert qu'à la
// suppression : le nom d'un instantané créé n'est jamais saisi.
export function actOnGuest(machineID: string, vmid: number, verb: GuestVerb, snapshot: string): Promise<ActionRun> {
  const body = verb === "snapshot-delete" ? { snapshot } : {};
  const params = { id: machineID, vmid: String(vmid) };
  return api.send<ActionRun>(guestRoute(verb, params), body);
}

function guestRoute(verb: GuestVerb, params: { id: string; vmid: string }) {
  switch (verb) {
    case "start":
      return route("POST /api/machines/{id}/proxmox/guests/{vmid}/actions/start", params);
    case "shutdown":
      return route("POST /api/machines/{id}/proxmox/guests/{vmid}/actions/shutdown", params);
    case "snapshot":
      return route("POST /api/machines/{id}/proxmox/guests/{vmid}/actions/snapshot", params);
    case "snapshot-delete":
      return route("POST /api/machines/{id}/proxmox/guests/{vmid}/actions/snapshot-delete", params);
  }
}
