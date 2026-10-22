import type { AuditEvent } from "../api/audit";
import type { Translate } from "../i18n/context";

// verbOf lit le verbe d'une action : ce qui suit la sorte d'objet.
export function verbOf(action: string): string {
  const dot = action.indexOf(".");
  return dot < 0 ? action : action.slice(dot + 1);
}

// actorLabel : la sorte d'acteur, puis son nom quand il en a un, puis pour
// qui un jeton d'API a agi quand mcp-maison le dit (on_behalf_of, indicatif).
export function actorLabel(t: Translate, event: Pick<AuditEvent, "actor_kind" | "actor_name" | "details">): string {
  const kind = t(`audit.actor_${event.actor_kind}`);
  const named = event.actor_name === "" ? kind : `${kind} · ${event.actor_name}`;
  const onBehalfOf = event.details.on_behalf_of;
  return onBehalfOf === undefined || onBehalfOf === "" ? named : `${named} · ${t("audit.on_behalf_of", onBehalfOf)}`;
}

// objectLabel : le nom recopié, sinon l'identifiant, sinon rien.
export function objectLabel(event: Pick<AuditEvent, "object_name" | "object_id">): string {
  if (event.object_name !== "") {
    return event.object_name;
  }
  return event.object_id;
}

// detailEntries rend les détails dans un ordre stable : celui des noms.
export function detailEntries(details: Record<string, string>): [string, string][] {
  return Object.entries(details).sort(([left], [right]) => left.localeCompare(right));
}
