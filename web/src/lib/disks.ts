import type { Disk } from "../api/types";
import { percentOf } from "./units";

// Les volumes d'une machine, la plus pleine en tête ; à part égale, par
// point de montage.
export function fullestFirst(disks: Disk[]): Disk[] {
  return [...disks].sort((a, b) => {
    const byFill = percentOf(b.used, b.total) - percentOf(a.used, a.total);
    return byFill !== 0 ? byFill : a.mount_point.localeCompare(b.mount_point);
  });
}
