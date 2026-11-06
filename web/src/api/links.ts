import { useResource, type Resource } from "../hooks/useResource";
import { api, segment } from "./client";
import { route } from "./routes";
import type { RightState } from "./machines";

// Les liens entre instances : qui utilise qui, d'où vient la preuve, depuis
// quand on ne l'a plus revu. Sujet du direct : « directory », que topicsOf
// devine du chemin.

export type LinkNature = "order" | "config" | "route" | "traffic";
// Déclaré : écrit par l'opérateur ou par la configuration d'un outil.
// Déduit : calculé à chaque lecture, l'opérateur le confirme ou l'écarte.
// Observé : une connexion vue dans la table de l'hôte (« host_connection »),
// gardée en mémoire par le serveur : jamais écrite, ni confirmée ni écartée.
export type LinkProof = "declared" | "deduced" | "observed";
// Le signal précis derrière la preuve. « public_url » : une adresse publique
// et un service qui fournit la capacité proxy (jamais écrit, seulement déduit).
// « environment » : l'environnement d'un conteneur nomme l'hôte de l'autre instance (droit observe.env).
// « host_connection » : un compte, une machine du réseau ou Internet a joint un port de l'hôte.
export type LinkSignal = "depends_on" | "container_link" | "operator" | "public_url" | "environment" | "host_connection";

// Le genre de ce qu'une extrémité désigne. Les trois derniers sont les sources
// d'une connexion observée : ce ne sont pas des instances, aucun service ne les porte.
export type LinkEndKind = "compose" | "container" | "systemd" | "account" | "address" | "internet";

// Une extrémité. Service et unité sont vides quand aucun service déclaré ne
// porte l'instance ; container_id quand un conteneur vivant la porte.
export interface LinkEnd {
  machine_id: string;
  machine_name: string;
  key: string;
  kind: LinkEndKind;
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
  // Une route déduite que vous n'avez pas encore décidée : à confirmer ou à écarter.
  proposed: boolean;
  // Une route déduite que l'opérateur a écartée : gardée pour être rétablie.
  dismissed: boolean;
  self: LinkEnd;
  peer: LinkEnd;
}

// Le droit de lire l'environnement des conteneurs sur la machine du service :
// « missing » quand la fenêtre « Ré-enrôler » le propose, « unknown » tant que
// l'agent n'a rien dit de ses droits. null : le service ne tourne pas
// dans des conteneurs.
interface EnvironmentRight {
  machine_id: string;
  right: RightState;
}

export interface ServiceLinksResponse {
  links: ServiceLink[];
  environment: EnvironmentRight | null;
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

// Les trois gestes sur un lien déduit : le reconnaître juste, l'écarter,
// rétablir un lien écarté.
export async function confirmLink(id: string): Promise<void> {
  return api.send(route("POST /api/directory/links/{id}/actions/confirm", { id }));
}

export async function dismissLink(id: string): Promise<void> {
  return api.send(route("POST /api/directory/links/{id}/actions/dismiss", { id }));
}

export async function restoreLink(id: string): Promise<void> {
  return api.send(route("POST /api/directory/links/{id}/actions/restore", { id }));
}
