import { useResource, type Resource } from "../hooks/useResource";
import { api } from "./client";

// L'accès de l'opérateur à l'interface. Chemin hors de toute ressource :
// topicsOf le relit sur tous les sujets sauf les mesures.

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
}

// Activer l'authentification révoque tous les jetons MCP : la réponse dit
// combien sont tombés.
export interface AuthEnableResponse extends AuthState {
  mcp_revoked: { api_tokens: number; sessions: number };
}

// Un compte et son mot de passe, ou l'en-tête d'un mandataire.
export type AuthEnableRequest = { username: string; password: string } | { mode: "forward"; header: string };

const authPath = "/api/auth";

export function useAuth(): Resource<AuthState> {
  return useResource<AuthState>(authPath);
}

export async function enableAuth(body: AuthEnableRequest): Promise<AuthEnableResponse> {
  return api.post<AuthEnableResponse>(`${authPath}/actions/enable`, body);
}

export async function disableAuth(): Promise<void> {
  return api.post(`${authPath}/actions/disable`);
}

export async function changePassword(password: string): Promise<void> {
  return api.put(`${authPath}/password`, { password });
}
