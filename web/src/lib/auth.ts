import type { AuthState } from "../api/auth";

// Ce que la barre latérale dit de l'accès : protégé par tel identifiant, ou
// par le mandataire pour telle identité. Rien tant que l'API n'a pas répondu,
// ni sans compte : l'écran de première configuration tient alors la place de
// l'interface.
export interface AccessLabel {
  key: string;
  username: string;
}

export function accessLabel(state: AuthState | null): AccessLabel | null {
  if (state === null || state.mode === "none") {
    return null;
  }
  if (state.mode === "forward") {
    return { key: "auth.side_protected_forward", username: state.username };
  }
  return { key: "auth.side_protected", username: state.username };
}

// Sans compte, le serveur ne sert que la première configuration : l'écran
// la remplace. Jamais sur un doute, ni pendant le chargement.
export function needsFirstSetup(state: AuthState | null): boolean {
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

// Le formulaire d'activation ou de changement : un identifiant (à
// l'activation seulement), le mot de passe, sa confirmation.
export interface CredentialsFormValues {
  username: string;
  password: string;
  confirmation: string;
}

export const emptyCredentialsForm: CredentialsFormValues = { username: "", password: "", confirmation: "" };

// L'en-tête que pose Authelia, proposé tel quel en mode mandataire.
export const defaultForwardHeader = "Remote-User";
