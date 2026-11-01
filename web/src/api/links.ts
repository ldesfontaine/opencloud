import { useResource, type Resource } from "../hooks/useResource";
import { api, segment } from "./client";
import { route } from "./routes";

// Les liens entre instances : qui utilise qui, d'où vient la preuve, depuis
// quand on ne l'a plus revu. Sujet du direct : « directory », que topicsOf
// devine du chemin.

export type LinkNature = "order" | "config" | "route" | "traffic";
// Déclaré : écrit par l'opérateur ou par la configuration d'un outil.
// Déduit et observé : calculé, ou vu ; aucune source ne les écrit encore.
export type LinkProof = "declared" | "deduced" | "observed";
// Le signal précis derrière la preuve.
export type LinkSignal = "depends_on" | "container_link" | "operator";

// Une extrémité. Service et unité sont vides quand aucun service déclaré ne
// porte l'instance ; container_id quand un conteneur vivant la porte.
export interface LinkEnd {
  machine_id: string;
  machine_name: string;
  key: string;
  label: string;
  service_id: string;
  service_name: string;
  unit_name: string;
  container_id: string;
}

// Un lien vu depuis un service : « outgoing », le service utilise l'autre ;
// « incoming », l'autre l'utilise.
export interface ServiceLink {
  id: string;
  direction: "outgoing" | "incoming";
  nature: LinkNature;
  proof: LinkProof;
  signal: LinkSignal;
  port: number;
  first_seen_at: string;
  last_seen_at: string;
  stale: boolean;
  removable: boolean;
  self: LinkEnd;
  peer: LinkEnd;
}

export interface ServiceLinksResponse {
  links: ServiceLink[];
}

// Ce que l'opérateur donne : un service de chaque côté, et l'unité quand le
// service en a plusieurs.
export interface LinkRequest {
  from_service_id: string;
  from_unit_id: string;
  to_service_id: string;
  to_unit_id: string;
  port: number;
}

export function useServiceLinks(serviceID: string): Resource<ServiceLinksResponse> {
  return useResource<ServiceLinksResponse>(() => `/api/directory/services/${segment(serviceID)}/links`);
}

export async function createLink(body: LinkRequest): Promise<void> {
  return api.send(route("POST /api/directory/links"), body);
}

export async function deleteLink(id: string): Promise<void> {
  return api.send(route("DELETE /api/directory/links/{id}", { id }));
}
