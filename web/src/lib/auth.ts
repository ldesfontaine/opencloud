import type { AuthState } from "../api/types";

// Ce que la barre latérale dit de l'accès : protégé par tel identifiant,
// par le mandataire pour telle identité, ou ouvert à qui atteint le port.
// Rien tant que l'API n'a pas répondu.
export interface AccessLabel {
  key: string;
  username: string | null;
}

export function accessLabel(state: AuthState | null): AccessLabel | null {
  if (state === null) {
    return null;
  }
  if (state.mode === "none") {
    return { key: "auth.side_open", username: null };
  }
  if (state.mode === "forward") {
    return { key: "auth.side_protected_forward", username: state.username };
  }
  return { key: "auth.side_protected", username: state.username };
}

// La bannière ne s'affiche que quand l'API a dit « none » : jamais sur un
// doute, ni pendant le chargement.
export function isInterfaceOpen(state: AuthState | null): boolean {
  return state !== null && state.mode === "none";
}

// Le mode mandataire est actif mais sans preuve du passage par le mandataire :
// la page des réglages le dit, sans couper l'accès.
export function isForwardInsecure(state: AuthState | null): boolean {
  return state !== null && state.mode === "forward" && state.forward_insecure === true;
}

// Vérifie le formulaire avant l'envoi, pour dire l'erreur sans attendre ;
// le serveur reste le juge. Rend la clé du catalogue, ou null.
export function passwordFormError(password: string, confirmation: string, minLength: number): string | null {
  if ([...password].length < minLength) {
    return "auth.password_too_short";
  }
  if (password !== confirmation) {
    return "auth.password_mismatch";
  }
  return null;
}
