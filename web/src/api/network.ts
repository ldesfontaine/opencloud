import { useResource, type Resource } from "../hooks/useResource";
import { segment } from "./client";
import type { Container, Engine } from "./containers";

// La topologie des conteneurs : réseaux, ports publiés, dépendances. Sujet
// du direct : « containers », que topicsOf devine du chemin.

// Un groupe du graphe : un réseau créé par l'opérateur ou par Compose, et
// les conteneurs que le serveur y a placés.
export interface Group {
  network_id: string;
  name: string;
  internal: boolean;
  members: string[];
}

// Une arête : d'Internet à un port publié, ou d'un conteneur à celui dont il
// dépend. Le nœud Internet a l'identifiant « internet ».
export interface Edge {
  from: string;
  to: string;
  kind: "public" | "depends";
  port?: number;
  protocol?: string;
  source?: "compose" | "link";
}

export interface NetworkResponse {
  machine_id: string;
  machine_name: string;
  online: boolean;
  engine: Engine | null;
  containers: Container[];
  groups: Group[];
  edges: Edge[];
}

// Toutes les machines, chacune avec sa topologie, pour la vue d'ensemble.
export interface OverviewNetworkResponse {
  machines: NetworkResponse[];
}

export function useNetworkOverview(): Resource<OverviewNetworkResponse> {
  return useResource<OverviewNetworkResponse>("/api/network");
}

export function useMachineNetwork(machineID: string): Resource<NetworkResponse> {
  return useResource<NetworkResponse>(() => `/api/machines/${segment(machineID)}/network`);
}
