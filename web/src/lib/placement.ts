import type { KindID, Machine } from "../api/machines";
import type { Translate } from "../i18n/context";
import { machineOption } from "./machines";
import type { SelectOption } from "./select";

// Les types de machine, dans l'ordre où on les propose (machine.Kinds côté Go).
export const kindIDs: readonly KindID[] = ["physical", "vm", "lxc", "vps", "unknown"];

// La correction que l'opérateur peut poser : le type et l'hôte. Vides, ils
// retirent la correction, et le type redevient celui qu'on déduit.
export interface PlacementValues {
  kindOverride: KindID | "";
  hostOverride: string;
}

export function placementOf(machine: Pick<Machine, "kind_override" | "host">): PlacementValues {
  return { kindOverride: machine.kind_override, hostOverride: machine.host?.source === "override" ? machine.host.machine_id : "" };
}

export function isPlacementModified(values: PlacementValues, initial: PlacementValues): boolean {
  return values.kindOverride !== initial.kindOverride || values.hostOverride !== initial.hostOverride;
}

// Les machines qu'on peut désigner comme hôte de celle-ci : toutes les autres,
// sauf celles qu'elle porte déjà, de près ou de loin. Le serveur refuserait la
// boucle ; autant ne pas la proposer.
export function hostCandidates(machine: Machine, all: readonly Machine[]): Machine[] {
  return all.filter((candidate) => candidate.id !== machine.id && !isCarriedBy(candidate, machine.id, all));
}

function isCarriedBy(candidate: Machine, machineID: string, all: readonly Machine[]): boolean {
  const seen = new Set<string>();
  let current: Machine | undefined = candidate;
  while (current?.host !== null && current?.host !== undefined && !seen.has(current.id)) {
    if (current.host.machine_id === machineID) {
      return true;
    }
    seen.add(current.id);
    const hostID: string = current.host.machine_id;
    current = all.find((other) => other.id === hostID);
  }
  return false;
}

// « Aucun » d'abord : retirer la correction est le premier choix de chaque liste.
export function hostOptions(t: Translate, none: string, candidates: readonly Machine[]): SelectOption[] {
  return [{ value: "", label: none }, ...candidates.map((candidate) => machineOption(t, candidate))];
}
