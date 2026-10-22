import { useResource, type Resource } from "../hooks/useResource";
import { api } from "./client";

// L'accès de l'opérateur à l'interface. Chemin sans sujet du direct : relu
// à la reconnexion, quand tout se relit, jamais à chaque sujet.

// « forward » est le mode derrière un mandataire (Authelia).
type AuthMode = "none" | "basic" | "forward";

// En forward, username est l'identité que le mandataire a transmise avec
// la requête, et header le nom de l'en-tête qui la porte.
export interface AuthState {
  mode: AuthMode;
  username: string;
  header?: string;
  enabled_at: string | null;
  password_min_length: number;
  // En forward, présent et vrai quand rien ne prouve que la requête passe
  // par le mandataire (pas de secret, boucle locale de confiance).
  forward_insecure?: boolean;
  // Les identités que forward_users (config.toml) admet, en forward
  // seulement ; null quand la liste est vide (toute identité du mandataire)
  // et dans les autres modes.
  allowed_users: string[] | null;
}

// Un compte et son mot de passe, ou l'en-tête d'un mandataire.
export type AuthEnableRequest = { username: string; password: string } | { mode: "forward"; header: string };

const authPath = "/api/auth";

export function useAuth(): Resource<AuthState> {
  return useResource<AuthState>(authPath);
}

// Crée le compte de la première configuration. Le serveur répond aussi
// combien de jetons d'API sont tombés : l'écran n'en dit rien, il n'y en a qu'après
// une commande de secours, et la documentation le dit.
export async function enableAuth(body: AuthEnableRequest): Promise<void> {
  await api.post(`${authPath}/actions/enable`, body);
}

// Confie au navigateur les identifiants que l'opérateur vient de choisir :
// une requête XHR qui les porte les fait entrer dans le cache HTTP Basic, et
// le navigateur les renvoie ensuite de lui-même, EventSource compris ; fetch
// ne sait pas le faire. Un navigateur qui ne les retient pas les demandera
// dans sa propre fenêtre : la promesse se résout dans tous les cas.
export function signIn(username: string, password: string): Promise<void> {
  return new Promise((resolve) => {
    const request = new XMLHttpRequest();
    request.open("GET", authPath, true, username, password);
    request.onloadend = () => resolve();
    request.send();
  });
}

export async function disableAuth(): Promise<void> {
  return api.post(`${authPath}/actions/disable`);
}

export async function changePassword(password: string): Promise<void> {
  return api.put(`${authPath}/password`, { password });
}
