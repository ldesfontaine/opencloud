import type { Machine } from "../api/machines";

// Ce que l'assistant attend de l'agent : une machine neuve qui apparaît sous
// son nom, ou la machine existante dont l'agent est remplacé.
// knownIDs : les machines déjà là quand le jeton est né, pour qu'un nom déjà
// pris par une autre ne passe pas pour l'arrivée de la nouvelle.
export type EnrollTarget =
  | { kind: "new"; name: string; knownIDs: string[] }
  | { kind: "reenroll"; machineID: string; previousAgentID: string | null };

export type EnrollProgress = { state: "waiting" } | { state: "expired" } | { state: "connected"; machine: Machine };

function hasActiveAgent(machine: Machine): boolean {
  return machine.agent !== null && machine.agent.revoked_at === null;
}

function hasArrived(target: EnrollTarget, machine: Machine): boolean {
  if (!hasActiveAgent(machine)) {
    return false;
  }
  if (target.kind === "new") {
    return machine.name === target.name && !target.knownIDs.includes(machine.id);
  }
  return machine.id === target.machineID && machine.agent?.id !== target.previousAgentID;
}

// Où en est l'enrôlement, lu dans la liste des machines que le direct relit.
// Un jeton expiré sans agent ne verra plus rien arriver : l'assistant le dit
// plutôt que d'attendre pour toujours.
export function enrollProgress(target: EnrollTarget, machines: readonly Machine[], expiresAt: number, now: number): EnrollProgress {
  const machine = machines.find((candidate) => hasArrived(target, candidate));
  if (machine !== undefined) {
    return { state: "connected", machine };
  }
  return now > expiresAt ? { state: "expired" } : { state: "waiting" };
}
