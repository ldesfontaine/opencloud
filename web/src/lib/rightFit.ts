import type { Machine, RightState } from "../api/machines";

// Où en est un droit sur l'agent d'une machine : le même jugement que le
// serveur (machine.Agent.Fit), vérifié sur une table de cas commune
// (internal/machine/testdata/right_fit_cases.json). Sans portée : le front ne
// juge que le droit, le serveur juge aussi le dossier ou le producteur.
interface RightFit {
  state: RightState;
  // La raison d'un droit coché mais pas exerçable.
  reason: string;
  // Le dossier d'un droit à un seul chemin (restic.target) ; vide sinon.
  path: string;
}

export function fitOf(agent: Machine["agent"], right: string): RightFit {
  const fit: RightFit = { state: "missing", reason: "", path: "" };
  if (agent === null || agent.revoked_at !== null) {
    return fit;
  }
  if (agent.reported_at === null) {
    return { ...fit, state: "unknown" };
  }
  const held = agent.capabilities.find((capability) => capability.name === right);
  if (held === undefined) {
    return fit;
  }
  const path = right !== "backup.produce" && held.paths.length === 1 ? (held.paths[0] ?? "") : "";
  if (held.unavailable !== "") {
    return { state: "unavailable", reason: held.unavailable, path };
  }
  return { state: "ok", reason: "", path };
}
