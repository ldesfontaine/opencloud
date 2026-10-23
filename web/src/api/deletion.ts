import { ApiError } from "./client";

// Le plan d'une suppression, tel que le serveur le rend avant le DELETE
// (GET …/delete-plan) et dans le corps du 409 qui la refuse : ce qui la
// retient, ce qui part avec l'objet, ce qui reste détaché.
interface DeletionObject {
  id: string;
  name: string;
}

export interface DeletionBlocker {
  kind: string;
  objects: DeletionObject[];
}

export interface DeletionCount {
  kind: string;
  count: number;
}

export interface DeletionPlan {
  blocked: boolean;
  blockers: DeletionBlocker[];
  removes: DeletionCount[];
  detaches: DeletionCount[];
}

// Le plan que porte le refus d'une suppression retenue (409), sinon null :
// un lien posé entre la lecture du plan et le DELETE se lit ainsi.
export function planOfRefusal(error: unknown): DeletionPlan | null {
  if (!(error instanceof ApiError) || error.status !== 409 || typeof error.payload !== "object" || error.payload === null || !("plan" in error.payload)) {
    return null;
  }
  return error.payload.plan as DeletionPlan;
}
