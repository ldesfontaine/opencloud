import type { DeletionBlocker, DeletionCount, DeletionPlan } from "../api/deletion";
import type { Translate } from "../i18n/context";
import { countedText } from "./manifest";

// « 2 sondes : site, api » : un genre d'objets qui retient la suppression,
// chacun nommé, pour que l'opérateur sache quoi supprimer ou détacher.
export function blockerText(t: Translate, language: string, blocker: DeletionBlocker): string {
  const names = blocker.objects.map((object) => object.name).join(", ");
  return `${countedText(t, language, `deletion.kind_${blocker.kind}`, blocker.objects.length)} : ${names}`;
}

// « 1 204 essais, 80 jours d'historique » : ce qui part ou se détache.
export function countsText(t: Translate, language: string, counts: DeletionCount[]): string {
  return counts.map((counted) => countedText(t, language, `deletion.kind_${counted.kind}`, counted.count)).join(", ");
}

// Un plan sans rien à retenir, emporter ni détacher : la suppression ne
// touche que l'objet.
export function isBarePlan(plan: DeletionPlan): boolean {
  return plan.blockers.length === 0 && plan.removes.length === 0 && plan.detaches.length === 0;
}
