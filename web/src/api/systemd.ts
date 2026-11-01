import { useResource, type Resource } from "../hooks/useResource";
import { segment } from "./client";

// Les unités systemd que les agents relèvent, en lecture seule. Sujet du
// direct : « machines », que topicsOf devine du chemin.

export type SystemdActive = "active" | "inactive" | "failed" | "activating" | "deactivating" | "reloading" | "other";
type SystemdReason = "no_systemctl" | "not_booted" | "unreadable";

// Le service déclaré qui porte l'unité ; nul : personne ne la déclare.
interface DeclaredUnitRef {
  service_id: string;
  service_name: string;
  unit_id: string;
}

// Une unité : son nom et ses trois états, rien d'autre.
export interface SystemdUnit {
  name: string;
  load: string;
  active: SystemdActive;
  sub: string;
  declared: DeclaredUnitRef | null;
}

// Le relevé d'une machine. reported_at est null tant que l'agent n'a rien dit ;
// available est faux quand systemd n'a pas pu être lu (reason dit pourquoi).
export interface SystemdReport {
  machine_id: string;
  machine_name: string;
  reported_at: string | null;
  available: boolean;
  reason: SystemdReason | "";
  truncated: boolean;
  units: SystemdUnit[];
}

export function useMachineSystemdUnits(machineID: string): Resource<SystemdReport> {
  return useResource<SystemdReport>(() => `/api/machines/${segment(machineID)}/systemd-units`);
}
