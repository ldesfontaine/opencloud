import type { Disk } from "../api/resources";
import type { DiskSettings } from "../api/settings";
import { percentOf } from "./units";

// Les volumes d'une machine, la plus pleine en tête ; à part égale, par
// point de montage.
export function fullestFirst(disks: Disk[]): Disk[] {
  return [...disks].sort((a, b) => {
    const byFill = percentOf(b.used, b.total) - percentOf(a.used, a.total);
    return byFill !== 0 ? byFill : a.mount_point.localeCompare(b.mount_point);
  });
}

// La règle du serveur, rejouée avant l'envoi : des entiers, l'attention
// d'au moins min_attention, le danger d'au plus max_danger, l'attention
// sous le danger. Rend la clé du refus, ou null.
export function diskSettingsError(
  thresholds: { attention: number; danger: number },
  bounds: Pick<DiskSettings, "min_attention" | "max_danger">,
): string | null {
  const { attention, danger } = thresholds;
  if (!Number.isInteger(attention) || !Number.isInteger(danger)) {
    return "settings.disk_invalid";
  }
  if (attention < bounds.min_attention || danger > bounds.max_danger || attention >= danger) {
    return "settings.disk_invalid";
  }
  return null;
}
