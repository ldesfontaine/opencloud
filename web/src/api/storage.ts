import { useResource, type Resource } from "../hooks/useResource";
import { segment } from "./client";

// Les disques physiques et les pools qu'une machine rapporte. Aucun relevé
// SMART : il viendra comme un droit à part de l'agent. Sujet du direct :
// « machines », que topicsOf devine du chemin.

export type DiskMedia = "nvme" | "ssd" | "hdd" | "virtual" | "unknown";
type PoolKind = "md" | "lvm" | "zfs";
export type PoolState = "ok" | "degraded" | "failed" | "unknown";

export interface PhysicalDisk {
  name: string;
  model: string;
  size: number;
  media: DiskMedia;
  removable: boolean;
}

// Un pool regroupe des disques : une grappe RAID du noyau, un groupe de volumes
// LVM, un pool ZFS. size vaut 0 quand le noyau ne le dit pas sans commande.
export interface StoragePool {
  name: string;
  kind: PoolKind;
  level: string;
  state: PoolState;
  size: number;
  members: string[];
}

export interface MachineStorage {
  disks: PhysicalDisk[];
  pools: StoragePool[];
}

export function useMachineStorage(machineID: string): Resource<MachineStorage> {
  return useResource<MachineStorage>(() => `/api/machines/${segment(machineID)}/storage`);
}
