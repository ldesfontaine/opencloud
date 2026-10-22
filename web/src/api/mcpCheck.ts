import { useResource, type Resource } from "../hooks/useResource";

// Ce que Paramètres dit de mcp-maison : s'il répond, sa version, combien
// d'outils openCloud il sert. Le serveur lit son /health à chaque lecture,
// sans sujet du direct : la carte relit à son ouverture et au bouton.

export type MCPCheckState = "not_configured" | "reachable" | "group_missing" | "unreachable" | "invalid_answer";

export interface MCPCheck {
  // L'adresse réglée (mcp_url) ; vide quand aucune ne l'est.
  url: string;
  state: MCPCheckState;
  // Vide avant v0.1.0 de mcp-maison.
  version: string;
  opencloud_tools: number;
  checked_at: string;
  // Ce qu'il faut écrire dans mcp_url pour qu'openCloud le cherche.
  suggested_url: string;
}

export function useMCPCheck(): Resource<MCPCheck> {
  return useResource<MCPCheck>("/api/settings/mcp");
}
