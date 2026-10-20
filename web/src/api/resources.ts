import { useResource, type Resource } from "../hooks/useResource";
import { segment } from "./client";

// Les mesures des machines : processeur, mémoire, disque, réseau. Sujet du
// direct : « resources », que topicsOf devine du chemin.

// Un volume réel de la machine, nommé par son point de montage.
export interface Disk {
  mount_point: string;
  device: string;
  used: number;
  total: number;
}

// Une lecture de l'agent : instant, pourcentage processeur, octets et
// octets par seconde ; les pourcentages de mémoire et de disque se calculent
// dans le front. disk_used et disk_total font la somme des volumes. Un point
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

function machineResourcesPath(machineID: string): string {
  return `/api/machines/${segment(machineID)}/resources`;
}

// La valeur courante de chaque machine.
export function useCurrentResources(): Resource<ResourcesResponse> {
  return useResource<ResourcesResponse>("/api/resources");
}

// machineID nul : rien à lire (l'onglet qui la montre n'est pas ouvert).
export function useMachineResources(machineID: string | null): Resource<Current> {
  return useResource<Current>(machineID === null ? null : () => machineResourcesPath(machineID));
}

export function useResourceHistory(machineID: string, timeWindow: WindowName): Resource<HistoryResponse> {
  return useResource<HistoryResponse>(() => `${machineResourcesPath(machineID)}/history?window=${timeWindow}`);
}
