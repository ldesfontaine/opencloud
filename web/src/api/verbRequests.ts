import { useResource, type Resource } from "../hooks/useResource";
import { api } from "./client";
import { route } from "./routes";

// Les demandes de verbe à confirmation : un jeton d'API a demandé un geste qui
// ne se défait pas (supprimer une machine). Rien n'est fait tant que
// l'opérateur n'a pas confirmé. Elles se relisent sur leur sujet du direct.

export interface VerbRequest {
  id: string;
  // L'identifiant du verbe du registre, « machine.delete ».
  verb: string;
  // L'objet visé, avec son nom à l'instant de la demande.
  object: { id: string; name: string };
  token_name: string;
  // Pour qui le jeton dit appeler ; vide si mcp-maison ne l'a pas dit.
  on_behalf_of: string;
  created_at: string;
  expires_at: string;
}

export interface VerbRequestsResponse {
  requests: VerbRequest[];
}

export function useVerbRequests(): Resource<VerbRequestsResponse> {
  return useResource<VerbRequestsResponse>("/api/verb-requests");
}

// Confirmer rejoue le geste : la réponse est celle de la route du geste (un
// refus 409 porte son plan).
export async function confirmVerbRequest(id: string): Promise<void> {
  return api.send(route("POST /api/verb-requests/{id}/actions/confirm", { id }));
}

export async function rejectVerbRequest(id: string): Promise<void> {
  return api.send(route("POST /api/verb-requests/{id}/actions/reject", { id }));
}
