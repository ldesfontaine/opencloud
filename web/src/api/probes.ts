import { useResource, type Resource } from "../hooks/useResource";
import { api, segment } from "./client";

// Les sondes : ce que l'API en rend, et tout ce que le front lui demande.
// Un chemin qui porte un identifiant passe par segment(), même quand
// l'identifiant vient du serveur. Sujet du direct : « probes », que
// topicsOf devine du chemin.

export type ProbeStatus = "new" | "up" | "degraded" | "down" | "paused";
export type ProbeOutcome = "up" | "degraded" | "down";
export type ProbeKind = "http" | "tcp";

// La chaîne présentée au dernier essai HTTPS, en faits. L'échéance et la
// confiance sont deux choses : un certificat d'autorité interne a une date
// parfaitement lisible. L'état et la pastille se dérivent dans
// lib/certificates.
export interface Certificate {
  subject: string;
  issuer: string;
  not_before: string;
  not_after: string;
  fingerprint: string;
  chain_valid: boolean;
  hostname_match: boolean;
  // Vide quand la cible n'agrafe rien : personne n'est contacté pour le
  // savoir.
  ocsp: "" | "good" | "revoked" | "unknown";
}

// Une sonde : des faits. La pastille, la disponibilité et les libellés se
// dérivent dans lib/probes. machine_online dit si quelqu'un sonde encore.
export interface Probe {
  id: string;
  name: string;
  kind: ProbeKind;
  target: string;
  machine_id: string;
  machine_name: string;
  machine_online: boolean;
  // Le service déclaré que la sonde surveille, sur n'importe quelle machine.
  service_id: string;
  service_name: string;
  service_machine_name: string;
  status: ProbeStatus;
  interval_seconds: number;
  timeout_seconds: number;
  failure_threshold: number;
  recovery_threshold: number;
  method: string;
  expected_status: string;
  expected_body: string;
  follow_redirects: boolean;
  // Une sonde TCP qui fait une poignée de main plutôt qu'une simple
  // connexion, pour lire le certificat d'un port qui ne parle pas HTTP.
  tls: boolean;
  last_checked_at: string | null;
  last_duration_ms: number;
  last_code: number | null;
  last_reason: string;
  certificate: Certificate | null;
  created_at: string;
}

// Un jour agrégé, en heure UTC : des comptes, jamais un pourcentage.
export interface ProbeDay {
  day: string;
  total: number;
  success: number;
  degraded: number;
  duration_ms: number;
}

export interface ProbeResult {
  checked_at: string;
  outcome: ProbeOutcome;
  duration_ms: number;
  code: number | null;
  reason: string;
}

// Une fenêtre de disponibilité : des essais et des succès ; le pourcentage
// se calcule dans le front, et une fenêtre sans essai n'en a pas.
export interface ProbeUptime {
  window: string;
  total: number;
  success: number;
}

export interface ProbesResponse {
  probes: Probe[];
  days: Record<string, ProbeDay[]>;
}

export interface ProbeResponse {
  probe: Probe;
  uptime: ProbeUptime[];
  days: ProbeDay[];
  results: ProbeResult[];
}

// Le corps de la création comme de l'édition : le serveur remplace la
// définition entière. lib/probes le construit depuis le formulaire.
export interface ProbeRequest {
  name: string;
  kind: ProbeKind;
  target: string;
  machine_id: string;
  service_id: string;
  interval_seconds: number;
  timeout_seconds: number;
  failure_threshold: number;
  recovery_threshold: number;
  method: string;
  expected_status: string;
  expected_body: string;
  follow_redirects: boolean;
  tls: boolean;
}

const probesPath = "/api/probes";

function probePath(id: string): string {
  return `${probesPath}/${segment(id)}`;
}

// Les sondes d'une machine : celles qu'elle exécute.
function machineProbesPath(machineID: string): string {
  return `/api/machines/${segment(machineID)}/probes`;
}

// wanted à faux : rien n'est lu, pour une liste qui ne sert que sous un choix.
export function useProbes(wanted = true): Resource<ProbesResponse> {
  return useResource<ProbesResponse>(wanted ? probesPath : null);
}

// id nul : rien à lire (un formulaire de création).
export function useProbe(id: string | null): Resource<ProbeResponse> {
  return useResource<ProbeResponse>(id === null ? null : () => probePath(id));
}

export function useMachineProbes(machineID: string): Resource<ProbesResponse> {
  return useResource<ProbesResponse>(() => machineProbesPath(machineID));
}

// Les écritures sont async : un segment() qui lève devient une promesse
// rejetée, que l'appelant affiche, jamais une exception qui bloque un bouton.

// Sans id, une création ; avec, le remplacement de cette sonde.
export async function saveProbe(id: string | undefined, body: ProbeRequest): Promise<Probe> {
  return id === undefined ? api.post<Probe>(probesPath, body) : api.put<Probe>(probePath(id), body);
}

export async function deleteProbe(id: string): Promise<void> {
  return api.delete(probePath(id));
}

export async function pauseProbe(id: string): Promise<void> {
  return api.post(`${probePath(id)}/actions/pause`);
}

export async function resumeProbe(id: string): Promise<void> {
  return api.post(`${probePath(id)}/actions/resume`);
}
