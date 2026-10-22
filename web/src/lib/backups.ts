import type { Backups } from "../api/backups";

// La longueur minimale d'une phrase, en caractères (le serveur compte des
// runes, espaces de bord retirés).
export const minPassphrase = 16;

// Pourquoi les deux saisies de la phrase ne passent pas, en clé du
// catalogue ; null si elles passent. Le serveur ne reçoit qu'une phrase :
// la confirmation se juge ici.
export function passphraseProblem(first: string, second: string): string | null {
  if (first !== second) {
    return "backup.passphrase_mismatch";
  }
  if ([...first.trim()].length < minPassphrase) {
    return "backup.passphrase_too_short";
  }
  return null;
}

// La base est sauvegardée quand une clé, un plan et la destination de ce
// plan existent : sinon la page le dit, en tête.
export function isBackedUp(backups: Backups): boolean {
  const plan = backups.plan;
  return backups.keyring !== null && plan !== null && backups.destinations.some((destination) => destination.id === plan.destination_id);
}
