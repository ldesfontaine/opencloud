import type { AuditEvent } from "../api/audit";
import type { Translate } from "../i18n/context";

// verbOf lit le verbe d'une action : ce qui suit la sorte d'objet.
export function verbOf(action: string): string {
  const dot = action.indexOf(".");
  return dot < 0 ? action : action.slice(dot + 1);
}

// actorLabel : la sorte d'acteur, puis son nom quand il en a un.
export function actorLabel(t: Translate, event: Pick<AuditEvent, "actor_kind" | "actor_name">): string {
  const kind = t(`audit.actor_${event.actor_kind}`);
  return event.actor_name === "" ? kind : `${kind} · ${event.actor_name}`;
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
