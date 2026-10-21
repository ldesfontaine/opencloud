import { useResource, type Resource } from "../hooks/useResource";
import { api, segment } from "./client";

// L'accès d'un agent IA par MCP : client OAuth, jetons d'API, sessions.
// Chemin sans sujet du direct : relu à la demande de la page, ou quand tout
// se relit.

// Un secret ou un jeton n'y figure que masqué ; le clair ne sort qu'à sa
// création.
export interface MCPState {
  enabled: boolean;
  client_id: string;
  secret_masked: string;
  redirect_uris: string[];
  endpoint: string;
  issuer: string;
  url_local: boolean;
  stdio_command: string;
  server_name: string;
  tokens: MCPToken[];
  sessions: MCPSession[];
  limits: { tokens: number; redirect_uris: number };
  // Les échéances offertes à un jeton d'API, en jours ; 0 pour sans échéance.
  lifetimes: { access_seconds: number; refresh_seconds: number; api_token_days: number[]; api_token_default_days: number };
  tools: { reads: number };
  enabled_at: string | null;
}

// La portée reste dans la base et dans l'API, inerte : le MCP HTTP est en
// lecture seule, quelle qu'elle soit. L'interface ne la montre plus.
type MCPScope = "read" | "write";

export interface MCPToken {
  id: string;
  name: string;
  masked: string;
  scope: MCPScope;
  created_at: string;
  // Nul pour un jeton créé sans échéance.
  expires_at: string | null;
  last_used_at: string | null;
}

// La portée qu'une session a reçue au consentement ; started_at est
// l'échange du code, qui le suit de quelques secondes.
export interface MCPSession {
  id: string;
  scope: MCPScope;
  started_at: string;
  last_used_at: string | null;
  expires_at: string;
}

interface MCPSecretResponse {
  secret: string;
}

export interface MCPTokenResponse {
  token: string;
  item: MCPToken;
}

// 0 jour : un jeton sans échéance.
export interface MCPTokenRequest {
  name: string;
  expires_in_days: number;
}

const mcpPath = "/api/mcp";

export function useMCP(): Resource<MCPState> {
  return useResource<MCPState>(mcpPath);
}

// Le secret du client OAuth revient en clair, cette fois seulement.
export async function enableMCP(): Promise<MCPSecretResponse> {
  return api.post<MCPSecretResponse>(`${mcpPath}/actions/enable`);
}

export async function regenerateMCPSecret(): Promise<MCPSecretResponse> {
  return api.post<MCPSecretResponse>(`${mcpPath}/actions/regenerate-secret`);
}

export async function disableMCP(): Promise<void> {
  return api.post(`${mcpPath}/actions/disable`);
}

export async function saveRedirectURIs(uris: string[]): Promise<MCPState> {
  return api.put<MCPState>(`${mcpPath}/redirect-uris`, { redirect_uris: uris });
}

// Le jeton revient en clair, cette fois seulement.
export async function createMCPToken(body: MCPTokenRequest): Promise<MCPTokenResponse> {
  return api.post<MCPTokenResponse>(`${mcpPath}/tokens`, body);
}

export async function deleteMCPToken(id: string): Promise<void> {
  return api.delete(`${mcpPath}/tokens/${segment(id)}`);
}

export async function deleteMCPSession(id: string): Promise<void> {
  return api.delete(`${mcpPath}/sessions/${segment(id)}`);
}

// Une demande d'autorisation qui attend l'opérateur, telle que le serveur
// la garde : la page ne lit rien d'autre de l'adresse que son identifiant.
// write_available est faux tant que le MCP HTTP est en lecture seule.
export interface MCPConsent {
  id: string;
  client_id: string;
  redirect_uri: string;
  write_requested: boolean;
  write_available: boolean;
  read_tools: number;
  access_seconds: number;
  refresh_seconds: number;
  expires_at: string;
}

// Où le navigateur part après la réponse : l'adresse de retour du client,
// avec le code ou access_denied.
interface MCPConsentAnswer {
  redirect: string;
}

export function useMCPConsent(id: string): Resource<MCPConsent> {
  return useResource<MCPConsent>(() => `${mcpPath}/consents/${segment(id)}`);
}

export async function allowMCPConsent(id: string, write: boolean): Promise<MCPConsentAnswer> {
  return api.post<MCPConsentAnswer>(`${mcpPath}/consents/${segment(id)}/actions/allow`, { write });
}

export async function denyMCPConsent(id: string): Promise<MCPConsentAnswer> {
  return api.post<MCPConsentAnswer>(`${mcpPath}/consents/${segment(id)}/actions/deny`);
}
