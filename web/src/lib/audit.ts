import type { AuditEvent } from "../api/audit";
import type { Translate } from "../i18n/context";
import type { Tone } from "./tone";

// verbOf lit le verbe d'une action : ce qui suit la sorte d'objet.
export function verbOf(action: string): string {
  const dot = action.indexOf(".");
  return dot < 0 ? action : action.slice(dot + 1);
}

// Le libellé de l'action d'une ligne. La révocation de tous les jetons, posée
// quand un compte est créé ou recréé, se dit en entier : sans cela, la
// première ligne d'un journal neuf, « Révocation », est une énigme (audit F-3).
export function actionLabel(t: Translate, event: Pick<AuditEvent, "action" | "details">): string {
  if (event.action === "api_token.revoke" && event.details.all === "true") {
    const revoked = event.details.api_tokens ?? "0";
    return revoked === "0" ? t("audit.verb_revoke_none") : t("audit.verb_revoke_all", revoked);
  }
  return t(`audit.verb_${verbOf(event.action)}`);
}

// Un objet qui n'a qu'une instance (l'accès à l'interface) se désigne par sa
// sorte : son nom technique répéterait le libellé (« Accès à l'interface
// interface »). Les lignes d'avant portent ce nom : on ne le montre pas.
const singletonKinds: ReadonlySet<string> = new Set(["auth"]);

export function isSingletonKind(kind: string): boolean {
  return singletonKinds.has(kind);
}

// L'acteur d'une ligne, décomposé pour l'écran : le genre (dont la pastille
// se déduit), le nom à montrer et, pour un jeton d'API, pour qui il agit.
// forWhom est null hors jeton ; "" quand le jeton n'a pas dit pour qui.
interface ActorParts {
  kind: AuditEvent["actor_kind"];
  name: string;
  forWhom: string | null;
}

export function actorParts(event: Pick<AuditEvent, "actor_kind" | "actor_name" | "actor_label" | "details">): ActorParts {
  // Le nom du jeton d'alors, sinon l'identifiant : une ligne d'avant la 017
  // se lit comme avant.
  const name = event.actor_label !== null && event.actor_label !== "" ? event.actor_label : event.actor_name;
  const forWhom = event.actor_kind === "api" ? (event.details.on_behalf_of ?? "") : null;
  return { kind: event.actor_kind, name, forWhom };
}

// actorLabel : le genre, puis le nom quand il y en a un, puis pour qui un
// jeton d'API a agi (on_behalf_of, indicatif : « inconnu » s'il ne le dit pas).
export function actorLabel(t: Translate, event: Pick<AuditEvent, "actor_kind" | "actor_name" | "actor_label" | "details">): string {
  const parts = actorParts(event);
  const kind = t(`audit.actor_${parts.kind}`);
  const named = parts.name === "" ? kind : `${kind} · ${parts.name}`;
  if (parts.forWhom === null) {
    return named;
  }
  return `${named} · ${parts.forWhom === "" ? t("audit.on_behalf_of_unknown") : t("audit.on_behalf_of", parts.forWhom)}`;
}

// actorTone : le repère visuel du genre. Le jeton d'API est la teinte forte
// (c'est lui qu'un agent IA emprunte) ; la personne, le vert ; un geste sans
// acteur, le danger.
export function actorTone(kind: AuditEvent["actor_kind"]): Tone {
  switch (kind) {
    case "api":
      return "accent";
    case "operator":
      return "ok";
    case "unknown":
      return "danger";
    default:
      return "neutral";
  }
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
