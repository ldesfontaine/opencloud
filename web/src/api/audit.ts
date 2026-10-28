import { useResource, type Resource } from "../hooks/useResource";

// Le journal d'audit : qui a fait quoi, quand, sur quel objet. Lecture
// seule, sans sujet du direct : la page relit à la demande. L'acteur, le
// verbe et la sorte d'objet se traduisent sous audit.*.

// « mcp » : les lignes d'avant le 21 octobre 2026, lues et jamais écrites.
export type AuditActorKind = "operator" | "api" | "cli" | "rule" | "agent" | "unknown" | "mcp";

export interface AuditEvent {
  id: number;
  at: string;
  actor_kind: AuditActorKind;
  // L'identifiant stable : celui du jeton, de la machine, le compte.
  actor_name: string;
  // Le nom du jeton d'API à l'instant du geste (il a pu être révoqué depuis) ;
  // null pour les autres acteurs et pour les lignes d'avant la migration 017.
  actor_label: string | null;
  // « sorte d'objet.verbe », par exemple probe.pause.
  action: string;
  object_kind: string;
  object_id: string;
  // Recopié au moment du geste : l'objet a pu changer de nom ou disparaître.
  object_name: string;
  // À plat, en texte, jamais un secret.
  details: Record<string, string>;
  apply_id: string | null;
}

export interface AuditResponse {
  events: AuditEvent[];
  // Le curseur de la page suivante ; null quand il n'y en a plus.
  next_before: number | null;
}

// Un objet à suivre : seule son histoire s'affiche.
export interface AuditObject {
  kind: string;
  id: string;
  name: string;
}

// Les filtres sur l'acteur : son genre (vide : tous) et un texte cherché dans
// le nom du jeton, son identifiant ou « pour qui ».
export interface AuditActorFilter {
  kind: AuditActorKind | "";
  search: string;
}

export const noActorFilter: AuditActorFilter = { kind: "", search: "" };

// Les genres qu'un filtre propose : « mcp » ne s'écrit plus, il n'est pas offert.
export const filterableActorKinds: readonly AuditActorKind[] = ["operator", "api", "agent", "rule", "cli", "unknown"];

export function auditPath(before: number | null, object: AuditObject | null, actor: AuditActorFilter = noActorFilter): string {
  const params = new URLSearchParams();
  if (actor.kind !== "") {
    params.set("actor_kind", actor.kind);
  }
  if (actor.search !== "") {
    params.set("actor", actor.search);
  }
  if (before !== null) {
    params.set("before", String(before));
  }
  if (object !== null) {
    params.set("object_kind", object.kind);
    params.set("object_id", object.id);
  }
  const query = params.toString();
  return query === "" ? "/api/audit" : `/api/audit?${query}`;
}

// Où en est l'envoi du journal vers les canaux : combien le reçoivent,
// combien de livraisons sont en échec. Le sujet du direct est « alerts »
// (cocher un canal le publie), que topicsOf devine du chemin.
export interface AuditForwarding {
  channels: number;
  failing: number;
}

export function useAuditForwarding(): Resource<AuditForwarding> {
  return useResource<AuditForwarding>("/api/audit/forwarding");
}

export function useAudit(before: number | null, object: AuditObject | null, actor: AuditActorFilter = noActorFilter): Resource<AuditResponse> {
  return useResource<AuditResponse>(auditPath(before, object, actor));
}
