import { useResource, type Resource } from "../hooks/useResource";
import { api, segment } from "./client";

// La page de statut : ses composants, ses incidents et ses réglages, côté
// opérateur, et ce que le public lit (statut/, qui a ses propres chemins).
// Sujet du direct : « status », que topicsOf devine du chemin.

// Un état est un mot d'une liste fermée ; vide quand rien ne compte. La
// pastille se dérive dans lib/status.
export type StatusState = "operational" | "degraded" | "down" | "maintenance" | "";
export type Impact = "degraded" | "down" | "maintenance";
export type IncidentStatus = "scheduled" | "investigating" | "identified" | "monitoring" | "in_progress" | "resolved";
export type MemberKind = "machine" | "service" | "heartbeat" | "probe";

// Un jour d'uptime d'un composant : la somme des jours de ses sondes.
export interface StatusDay {
  day: string;
  total: number;
  success: number;
  degraded: number;
}

interface IncidentUpdate {
  status: IncidentStatus;
  message: string;
  created_at: string;
}

// Un objet rattaché, tel que l'opérateur le voit : jamais servi au public.
export interface StatusMember {
  kind: MemberKind;
  id: string;
  name: string;
  state: StatusState;
  counts: boolean;
  present: boolean;
}

export interface StatusComponent {
  id: string;
  name: string;
  position: number;
  members: StatusMember[];
  derived: StatusState;
  effective: StatusState;
  created_at: string;
}

export interface StatusComponentsResponse {
  public_url: string;
  global: StatusState;
  components: StatusComponent[];
}

export interface Incident {
  id: string;
  title: string;
  impact: Impact;
  status: IncidentStatus;
  starts_at: string | null;
  ends_at: string | null;
  components: { id: string; name: string }[];
  updates: IncidentUpdate[];
  created_at: string;
  updated_at: string;
  resolved_at: string | null;
}

export interface IncidentsResponse {
  incidents: Incident[];
}

export interface StatusPageSettings {
  title: string;
  announcement: string;
  language: string;
  public_url: string;
}

// Ce que le public lit : les noms des composants, jamais les objets.
export interface PublicComponent {
  id: string;
  name: string;
  state: StatusState;
  days: StatusDay[];
}

export interface PublicIncident {
  id: string;
  title: string;
  impact: Impact;
  status: IncidentStatus;
  starts_at: string | null;
  ends_at: string | null;
  components: string[];
  updates: IncidentUpdate[];
  created_at: string;
  updated_at: string;
  resolved_at: string | null;
}

export interface PublicStatus {
  title: string;
  announcement: string;
  language: string;
  generated_at: string;
  global: StatusState;
  components: PublicComponent[];
  open: PublicIncident[];
  history: PublicIncident[];
}

export interface StatusComponentRequest {
  name: string;
  position: number;
  members: { kind: MemberKind; id: string }[];
}

// Une maintenance porte sa fenêtre ; un incident, des dates nulles.
export interface IncidentRequest {
  title: string;
  impact: Impact;
  status: IncidentStatus;
  message: string;
  component_ids: string[];
  starts_at: string | null;
  ends_at: string | null;
}

// Ce qui se corrige d'un incident : le titre, les composants, la fenêtre.
export interface IncidentEditRequest {
  title: string;
  component_ids: string[];
  starts_at: string | null;
  ends_at: string | null;
}

export interface IncidentUpdateRequest {
  status: IncidentStatus;
  message: string;
}

export interface StatusPageRequest {
  title: string;
  announcement: string;
  language: string;
}

const statusPath = "/api/status";
const componentsPath = `${statusPath}/components`;
const incidentsPath = `${statusPath}/incidents`;
const pageSettingsPath = `${statusPath}/page`;

function componentPath(id: string): string {
  return `${componentsPath}/${segment(id)}`;
}

function incidentPath(id: string): string {
  return `${incidentsPath}/${segment(id)}`;
}

export function useStatusComponents(): Resource<StatusComponentsResponse> {
  return useResource<StatusComponentsResponse>(componentsPath);
}

// id nul : rien à lire (un formulaire de création).
export function useStatusComponent(id: string | null): Resource<StatusComponent> {
  return useResource<StatusComponent>(id === null ? null : () => componentPath(id));
}

export function useIncidents(): Resource<IncidentsResponse> {
  return useResource<IncidentsResponse>(incidentsPath);
}

export function useIncident(id: string): Resource<Incident> {
  return useResource<Incident>(() => incidentPath(id));
}

export function useStatusPageSettings(): Resource<StatusPageSettings> {
  return useResource<StatusPageSettings>(pageSettingsPath);
}

// Sans id, une création ; avec, le remplacement de ce composant.
export async function saveStatusComponent(id: string | undefined, body: StatusComponentRequest): Promise<StatusComponent> {
  return id === undefined ? api.post<StatusComponent>(componentsPath, body) : api.put<StatusComponent>(componentPath(id), body);
}

export async function deleteStatusComponent(id: string): Promise<void> {
  return api.delete(componentPath(id));
}

export async function createIncident(body: IncidentRequest): Promise<Incident> {
  return api.post<Incident>(incidentsPath, body);
}

export async function editIncident(id: string, body: IncidentEditRequest): Promise<Incident> {
  return api.put<Incident>(incidentPath(id), body);
}

// Une entrée du fil : un statut, un message, ou les deux.
export async function addIncidentUpdate(id: string, body: IncidentUpdateRequest): Promise<Incident> {
  return api.post<Incident>(`${incidentPath(id)}/updates`, body);
}

export async function deleteIncident(id: string): Promise<void> {
  return api.delete(incidentPath(id));
}

export async function saveStatusPageSettings(body: StatusPageRequest): Promise<StatusPageSettings> {
  return api.put<StatusPageSettings>(pageSettingsPath, body);
}
