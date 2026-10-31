import { useResource, type Resource } from "../hooks/useResource";
import { api, segment } from "./client";

// Les jetons d'API : ce qu'un jeton lit de l'API, pour mcp-maison ou un
// script, et en écriture les verbes qu'il peut déclencher. Chemin sans sujet du direct : relu à la demande de la page, ou
// quand tout se relit. Le jeton n'y figure que masqué ; le clair ne sort
// qu'à sa création.

export interface APIToken {
  id: string;
  name: string;
  masked: string;
  scope: "read" | "write";
  // Les verbes déclenchés, vide pour une lecture ou une écriture sans case cochée.
  verbs: string[];
  // Où les verbes portent : null pour toutes les machines (les invités
  // Proxmox), une liste sinon, vide pour aucune.
  machines: string[] | null;
  guests: number[] | null;
  created_at: string;
  // Nul pour un jeton créé sans échéance.
  expires_at: string | null;
  last_used_at: string | null;
}

// Un verbe offert à la création : label et summary sont des clés de
// catalogue, reverse l'identifiant du verbe qui le défait.
export interface VerbOffer {
  id: string;
  label: string;
  summary: string;
  reverse: string;
}

// Ce que mcp-maison lit dans son .env, tiré de l'adresse d'écoute.
export interface APITokenEnv {
  mcp_host: string;
  opencloud_url: string;
}

// Une machine que la liste à cocher offre.
export interface ReachMachine {
  id: string;
  name: string;
}

export interface APITokensState {
  tokens: APIToken[];
  // Les machines connues, par nom, pour limiter un jeton.
  machines: ReachMachine[];
  limit: number;
  // Les échéances offertes, en jours ; 0 pour sans échéance.
  lifetimes: { days: number[]; default_days: number };
  verbs: VerbOffer[];
  env: APITokenEnv;
}

export interface APITokenResponse {
  token: string;
  item: APIToken;
}

export type APITokenScope = APIToken["scope"];

// 0 jour : un jeton sans échéance. Les verbes ne vont qu'avec l'écriture.
export interface APITokenRequest extends ReachRequest {
  name: string;
  expires_in_days: number;
  scope: APITokenScope;
  verbs?: string[];
}

// Une clé absente vaut « toutes » ; une liste, même vide, limite.
export interface ReachRequest {
  machines?: string[];
  guests?: number[];
}

const apiTokensPath = "/api/settings/api-tokens";

export function useAPITokens(): Resource<APITokensState> {
  return useResource<APITokensState>(apiTokensPath);
}

// Le jeton revient en clair, cette fois seulement.
export async function createAPIToken(body: APITokenRequest): Promise<APITokenResponse> {
  return api.post<APITokenResponse>(apiTokensPath, body);
}

export async function deleteAPIToken(id: string): Promise<void> {
  return api.delete(`${apiTokensPath}/${segment(id)}`);
}

// Remplace la portée du jeton : ce que la requête ne dit pas redevient « toutes ».
export async function setAPITokenReach(id: string, body: ReachRequest): Promise<APIToken> {
  return api.put<APIToken>(`${apiTokensPath}/${segment(id)}/reach`, body);
}
