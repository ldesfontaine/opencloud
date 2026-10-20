import { useResource, type Resource } from "../hooks/useResource";
import { segment } from "./client";

// Le fichier de l'annuaire : un plan est ce que le fichier changerait, un
// passage (Apply) ce qui a été écrit. Les clés d'état, d'action, de genre et
// de conséquence se traduisent sous manifest.*. Sujet du direct :
// « directory », que topicsOf devine du chemin.

export type ManifestAction = "create" | "update" | "delete";
export type ManifestKind = "project" | "service" | "probe" | "heartbeat" | "ignored";
export type ApplyState = "confirmed" | "applied" | "failed";
export type ActorKind = "cli" | "operator";

export interface ManifestFieldChange {
  field: string;
  before: string;
  after: string;
}

export interface ManifestCarry {
  // Le genre de ce qu'une suppression emporte : probe_results, units…
  what: string;
  count: number;
}

export interface ManifestChange {
  action: ManifestAction;
  kind: ManifestKind;
  // Vide pour une création : l'objet n'existe pas encore.
  id: string;
  name: string;
  fields: ManifestFieldChange[];
  notes: string[];
  carries: ManifestCarry[];
}

export interface ManifestProblem {
  key: string;
  line: number;
  column: number;
  path: string;
  value: string;
}

export interface ManifestPlan {
  language: string;
  file_hash: string;
  prune: boolean;
  plan_hash: string;
  applicable: boolean;
  summary: { create: number; update: number; delete: number; unchanged: number; unmanaged: number };
  changes: ManifestChange[];
  unmanaged: Array<{ kind: ManifestKind; id: string; name: string }>;
  problems: ManifestProblem[];
}

// Un passage du journal. Les changements ne sont que dans le détail.
export interface Apply {
  id: string;
  at: string;
  finished_at: string | null;
  actor_kind: ActorKind;
  actor_name: string;
  file_hash: string;
  file_name: string;
  prune: boolean;
  plan_hash: string;
  state: ApplyState;
  summary: { create: number; update: number; delete: number; unchanged: number };
  done: number;
  error: string;
  error_object: string;
  changes?: ManifestChange[];
}

export interface AppliesResponse {
  applies: Apply[];
}

const appliesPath = "/api/manifest/applies";

// Le fichier de l'annuaire, à télécharger par un lien.
export const manifestExportPath = "/api/manifest/export";

// Le même fichier avec, en plus, ce que les conteneurs non déclarés
// laissent deviner.
export const proposedExportPath = `${manifestExportPath}?propose=1`;

export function useApplies(): Resource<AppliesResponse> {
  return useResource<AppliesResponse>(appliesPath);
}

export function useApply(id: string): Resource<Apply> {
  return useResource<Apply>(() => `${appliesPath}/${segment(id)}`);
}
