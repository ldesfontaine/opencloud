import { useResource, type Resource } from "../hooks/useResource";
import { segment } from "./client";
import type { Container, Engine } from "./containers";
import type { LinkNature, LinkProof, LinkSignal } from "./links";

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

// Une arête : d'Internet à un port publié, ou d'un conteneur à celui qu'un
// lien stocké désigne. Le nœud Internet a l'identifiant « internet ». Une
// arête de lien dit son identifiant, sa nature, sa preuve et si elle est
// ancienne ; une arête publique ne dit rien de tout cela.
export interface Edge {
  from: string;
  to: string;
  kind: "public" | "depends";
  port?: number;
  protocol?: string;
  link_id?: string;
  nature?: LinkNature;
  proof?: LinkProof;
  signal?: LinkSignal;
  stale?: boolean;
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

// Le type d'une machine de la carte : celui que la fiche retient.
export type MapKind = "physical" | "vm" | "lxc" | "vps" | "unknown";

// D'où le nom d'une zone vient : le pare-feu, un tag VLAN, ou le sous-réseau.
export type ZoneSource = "firewall" | "vlan" | "subnet";

// Ce que dit la pastille d'un service ; « missing » : déclaré, la machine ne le
// montre pas.
export type ServiceState = "running" | "degraded" | "stopped" | "failed" | "missing" | "unknown";

export interface MapZone {
  id: string;
  name: string;
  source: ZoneSource;
  cidr: string;
  vlan: number;
  // La teinte stable, de 0 à 7.
  tone: number;
}

// Une puce : un service dans sa machine. Elle mène au service déclaré s'il y
// en a un, sinon au conteneur.
export interface MapService {
  id: string;
  label: string;
  kind: "compose" | "container" | "systemd";
  state: ServiceState;
  replicas: number;
  container_id: string;
  service_id: string;
}

// Une machine. managed : un agent la lit ; faux pour un invité que seul son
// hôte connaît. host_id : la carte de son hôte, vide sinon ; guest_count : les
// invités d'un hôte ; folded : l'hôte se présente replié (ses invités cachés).
export interface MapBox {
  id: string;
  machine_id: string;
  name: string;
  kind: MapKind;
  vmid: number;
  node: string;
  managed: boolean;
  online: boolean;
  guest_status: string;
  address: string;
  zone_id: string;
  services: MapService[];
  host_id: string;
  guest_count: number;
  folded: boolean;
}

// Le couloir d'une zone : les machines qui y sont, les hôtes d'abord. Un
// zone_id vide est le couloir des machines que rien ne place.
export interface MapLane {
  zone_id: string;
  boxes: MapBox[];
}

export interface MapInterface {
  name: string;
  device: string;
  role: string;
  cidr: string;
  up: boolean;
  zone_id: string;
  wan: boolean;
  tunnel: boolean;
}

export interface MapFirewall {
  id: string;
  name: string;
  interfaces: MapInterface[];
}

export interface MapTunnel {
  id: string;
  label: string;
  proof: LinkProof;
  outside: string;
  service: string;
  firewall: string;
  interface: string;
}

export interface MapEntry {
  to: string;
  ports: number[];
  observed: boolean;
}

export interface MapLink {
  id: string;
  from: string;
  to: string;
  nature: LinkNature;
  proof: LinkProof;
  signal: LinkSignal;
  port: number;
  stale: boolean;
  // Une route déduite que l'opérateur n'a pas encore décidée.
  proposed: boolean;
  first_seen_at: string;
  last_seen_at: string;
}

// La carte de la vue d'ensemble : Internet (les entrées), l'extérieur (à plat),
// la maison (un couloir par zone qui contient une machine), ce qui les relie.
// empty_zones : les zones où aucune machine ne tourne. folding : la carte est
// assez grande pour que les hôtes se replient.
export interface NetworkMap {
  zones: MapZone[];
  outside: MapBox[];
  home: MapLane[];
  empty_zones: string[];
  folding: boolean;
  firewalls: MapFirewall[];
  tunnels: MapTunnel[];
  entries: MapEntry[];
  links: MapLink[];
}

export function useNetworkMap(): Resource<NetworkMap> {
  return useResource<NetworkMap>("/api/network");
}

export function useMachineNetwork(machineID: string): Resource<NetworkResponse> {
  return useResource<NetworkResponse>(() => `/api/machines/${segment(machineID)}/network`);
}
