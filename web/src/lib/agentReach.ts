import type { Machine } from "../api/machines";

// Un geste qui part par l'agent (un ordre, les journaux, une vérification)
// n'a de sens que machine en ligne : il passe par le flux ouvert de son agent.
// Rend, parmi les machines demandées, celles que la liste dit hors ligne ; une
// machine que la liste ne connaît pas (pas encore lue) ne bloque rien, le
// serveur jugera.
export function offlineAmong(machines: readonly Pick<Machine, "id" | "online">[], wanted: readonly string[]): string[] {
  return wanted.filter((id) => machines.some((machine) => machine.id === id && !machine.online));
}
