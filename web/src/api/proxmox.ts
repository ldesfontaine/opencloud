import { useResource, type Resource } from "../hooks/useResource";
import type { ActionRun } from "./actions";
import { api, segment } from "./client";

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

export interface ProxmoxHost {
  api_url: string;
  token_id: string;
  // L'empreinte SHA-256 épinglée ; vide : le magasin du système juge.
  pin: string;
  node: string;
  version: string;
  verified_at: string;
  last_read_at: string | null;
  // La clé du refus du dernier relevé qui a échoué ; vide s'il a réussi.
  last_error: string;
  guests: ProxmoxGuest[];
  storages: ProxmoxStorage[];
}

// La VM qu'une machine enrôlée est sur un hôte Proxmox.
export interface ProxmoxHosted {
  host_machine_id: string;
  host_machine_name: string;
  vmid: number;
  kind: ProxmoxKind;
  name: string;
  status: string;
  cpus: number;
  max_memory: number;
  last_backup_at: string | null;
  last_backup_bytes: number;
}

// Ce que la liste des machines dit de Proxmox pour chacune.
export interface ProxmoxSummary {
  declared: boolean;
  guests: number;
  last_error: string;
  hosted: ProxmoxHosted | null;
}

export interface ProxmoxDefinition {
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
  return api.put<ProxmoxHost>(`/api/machines/${segment(machineID)}/proxmox`, definition);
}

export async function removeMachineProxmox(machineID: string): Promise<void> {
  return api.post(`/api/machines/${segment(machineID)}/actions/remove-proxmox`);
}

// Un geste sur un invité, fait par l'agent de l'hôte : démarrer, arrêter
// proprement, prendre un instantané, supprimer un instantané de ce produit.
export type GuestVerb = "start" | "shutdown" | "snapshot" | "snapshot-delete";

// L'ordre part vers l'agent de la machine qui porte l'hôte ; la réponse est son
// run, que le direct (« actions ») fait avancer. snapshot ne sert qu'à la
// suppression : le nom d'un instantané créé n'est jamais saisi.
export function actOnGuest(machineID: string, vmid: number, verb: GuestVerb, snapshot: string): Promise<ActionRun> {
  const body = verb === "snapshot-delete" ? { snapshot } : {};
  return api.post<ActionRun>(`/api/machines/${segment(machineID)}/proxmox/guests/${vmid}/actions/${verb}`, body);
}
