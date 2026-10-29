import { inFlightStates, type ActionRun } from "../api/actions";

// Où en est la dernière mise à jour (ou le dernier retour arrière) de l'agent
// d'une machine, lue dans ses derniers ordres. Aucune mémoire côté navigateur :
// tout vient du serveur.
export type AgentUpdateState =
  // L'ordre est parti ou accepté : la nouvelle version a trois minutes pour être confirmée.
  | { kind: "pending" }
  | { kind: "confirmed" }
  // L'agent est revenu seul à la version précédente ; target est la version refusée.
  | { kind: "rolled_back"; target: string; cause: "timeout" | "crash" }
  // La remise en état a échoué à son tour.
  | { kind: "rollback_failed" };

const updateKinds: ReadonlySet<string> = new Set(["agent.update", "agent.rollback"]);

// La plus récente des mises à jour et des retours, du plus récent au plus
// ancien (l'ordre de l'API). null : rien de récent à dire, ou un refus qui n'a
// rien changé (l'ordre n'a pas touché l'agent).
export function agentUpdateState(runs: ActionRun[]): AgentUpdateState | null {
  const latest = runs.find((run) => updateKinds.has(run.kind));
  if (latest === undefined) {
    return null;
  }
  if (inFlightStates.has(latest.state)) {
    return { kind: "pending" };
  }
  if (latest.state === "succeeded") {
    return { kind: "confirmed" };
  }
  if (latest.state !== "failed") {
    return null;
  }
  if (latest.reason === "rollback_failed") {
    return { kind: "rollback_failed" };
  }
  if (latest.reason === "rolled_back_timeout" || latest.reason === "rolled_back_crash") {
    const target = typeof latest.params.version === "string" ? latest.params.version : "";
    return { kind: "rolled_back", target, cause: latest.reason === "rolled_back_crash" ? "crash" : "timeout" };
  }
  return null;
}
