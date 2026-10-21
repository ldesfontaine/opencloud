import { useResource, type Resource } from "../hooks/useResource";

// Le journal d'audit : qui a fait quoi, quand, sur quel objet. Lecture
// seule, sans sujet du direct : la page relit à la demande. L'acteur, le
// verbe et la sorte d'objet se traduisent sous audit.*.

type AuditActorKind = "operator" | "mcp" | "cli" | "rule" | "agent" | "unknown";

export interface AuditEvent {
  id: number;
  at: string;
  actor_kind: AuditActorKind;
  actor_name: string;
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

export function auditPath(before: number | null, object: AuditObject | null): string {
  const params = new URLSearchParams();
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

export function useAudit(before: number | null, object: AuditObject | null): Resource<AuditResponse> {
  return useResource<AuditResponse>(auditPath(before, object));
}
