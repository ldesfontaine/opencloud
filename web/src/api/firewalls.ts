import { useResource, type Resource } from "../hooks/useResource";
import { api, segment } from "./client";
import { route } from "./routes";

// Les pare-feu déclarés et ce que leur API en dit. Sujet du direct :
// « firewalls », que topicsOf devine du chemin. Le secret de l'API n'est jamais
// rendu par le serveur : il ne sort d'ici que dans la requête qui déclare le
// pare-feu. Un jeton d'API (mcp-maison) ne voit ni la clé ni l'empreinte ; ce
// front est celui de l'opérateur.

export type FirewallKind = "opnsense";

export const firewallKinds: readonly FirewallKind[] = ["opnsense"];

export interface FirewallCounts {
  interfaces: number;
  rules: number;
  leases: number;
  gateways: number;
}

export interface Firewall {
  id: string;
  name: string;
  kind: FirewallKind;
  api_url: string;
  // Les quatre premiers caractères de la clé : le serveur ne la rend jamais en entier.
  api_key_hint: string;
  // L'empreinte SHA-256 épinglée ; vide : le magasin du système juge.
  pin: string;
  is_pinned: boolean;
  version: string;
  verified_at: string;
  last_read_at: string | null;
  // La clé du refus du dernier relevé qui a échoué ; vide s'il a réussi.
  last_error: string;
  counts: FirewallCounts;
}

export interface FirewallsResponse {
  firewalls: Firewall[];
  limit: number;
}

// online, offline, degraded ou unknown : les mots d'openCloud, quel que soit
// le constructeur.
export type GatewayStatus = "online" | "offline" | "degraded" | "unknown";

export interface FirewallGateway {
  name: string;
  address: string;
  status: GatewayStatus;
  // Nuls : le pare-feu ne sonde pas cette passerelle.
  delay_ms: number | null;
  loss_percent: number | null;
}

export interface FirewallDetail extends Firewall {
  gateways: FirewallGateway[];
}

// Ce que chaque relevé rappelle du pare-feu : de quand il date.
interface FirewallReading {
  id: string;
  name: string;
  last_read_at: string | null;
  last_error: string;
}

export interface FirewallInterface {
  name: string;
  device: string;
  role: string;
  address: string;
  status: string;
}

export interface FirewallInterfacesResponse extends FirewallReading {
  interfaces: FirewallInterface[];
}

export interface FirewallRule {
  interface: string;
  action: string;
  direction: string;
  protocol: string;
  source: string;
  source_port: string;
  destination: string;
  destination_port: string;
  description: string;
  is_enabled: boolean;
}

export interface FirewallRulesResponse extends FirewallReading {
  rules: FirewallRule[];
}

// La machine d'openCloud que le bail désigne, par sa carte réseau ou son adresse.
interface FirewallLeaseMachine {
  id: string;
  name: string;
}

export interface FirewallLease {
  // Nulle quand aucune machine connue ne correspond.
  machine: FirewallLeaseMachine | null;
  address: string;
  mac: string;
  hostname: string;
  interface: string;
  // dnsmasq, kea ou isc.
  server: string;
  is_static: boolean;
  expires_at: string | null;
}

export interface FirewallLeasesResponse extends FirewallReading {
  leases: FirewallLease[];
}

// Le modèle des échanges, indépendant du pare-feu (internal/policy) : des zones,
// des objets que les règles désignent, et la grille « qui parle à qui ».
type ZoneKind = "network" | "self" | "internet";

export interface FlowZone {
  id: string;
  // Vide pour une zone que le serveur a ajoutée (self, internet) : l'écran la nomme.
  label: string;
  kind: ZoneKind;
  network: string;
  is_up: boolean;
}

type FlowObjectKind = "any" | "self" | "zone" | "host" | "network" | "named";

export interface FlowObject {
  id: string;
  kind: FlowObjectKind;
  label: string;
  zone: string;
  is_negated: boolean;
}

export interface FlowEndpoint {
  // L'identifiant d'une zone, « * » pour toute zone, vide si la règle ne la dit pas.
  zone: string;
  object: string;
  except: string;
}

export interface FlowRule {
  ref: string;
  from: FlowEndpoint;
  to: FlowEndpoint;
  // Vides : tous.
  protocol: string;
  port: string;
  source_port: string;
  why: string;
  effect: "allow" | "deny";
  is_enabled: boolean;
}

export interface FlowCell {
  from: string;
  to: string;
  allowed: FlowRule[];
  denied: FlowRule[];
}

export interface FirewallFlowsResponse extends FirewallReading {
  zones: FlowZone[];
  objects: FlowObject[];
  // Les zones de départ (lignes) et d'arrivée (colonnes). Une case absente de
  // cells est une case vide : refusé.
  rows: string[];
  columns: string[];
  cells: FlowCell[];
  unplaced: FlowRule[];
  disabled: number;
}

interface FirewallDefinition {
  name: string;
  kind: FirewallKind;
  api_url: string;
  api_key: string;
  api_secret: string;
  pin: string;
}

export function useFirewalls(): Resource<FirewallsResponse> {
  return useResource<FirewallsResponse>("/api/firewalls");
}

export function useFirewall(id: string | null): Resource<FirewallDetail> {
  return useResource<FirewallDetail>(id === null ? null : () => `/api/firewalls/${segment(id)}`);
}

export function useFirewallInterfaces(id: string): Resource<FirewallInterfacesResponse> {
  return useResource<FirewallInterfacesResponse>(() => `/api/firewalls/${segment(id)}/interfaces`);
}

export function useFirewallRules(id: string): Resource<FirewallRulesResponse> {
  return useResource<FirewallRulesResponse>(() => `/api/firewalls/${segment(id)}/rules`);
}

export function useFirewallLeases(id: string): Resource<FirewallLeasesResponse> {
  return useResource<FirewallLeasesResponse>(() => `/api/firewalls/${segment(id)}/leases`);
}

export function useFirewallFlows(id: string): Resource<FirewallFlowsResponse> {
  return useResource<FirewallFlowsResponse>(() => `/api/firewalls/${segment(id)}/flows`);
}

export function declareFirewall(definition: FirewallDefinition): Promise<FirewallDetail> {
  return api.send<FirewallDetail>(route("POST /api/firewalls"), definition);
}

export function redeclareFirewall(id: string, definition: FirewallDefinition): Promise<FirewallDetail> {
  return api.send<FirewallDetail>(route("PUT /api/firewalls/{id}", { id }), definition);
}

export async function removeFirewall(id: string): Promise<void> {
  return api.send(route("DELETE /api/firewalls/{id}", { id }));
}
