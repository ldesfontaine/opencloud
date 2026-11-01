import { useResource, type Resource } from "../hooks/useResource";
import { api, segment } from "./client";
import { route } from "./routes";
import type { DeletionPlan } from "./deletion";

// La disponibilité publique : les composants de chaque projet publié, les
// incidents qui les touchent, côté opérateur, et ce que le public lit
// (statut/, qui a ses propres chemins). La page d'un projet se publie par le
// projet (api/directory). Sujet du direct : « status », que topicsOf devine du
// chemin.

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
  project_id: string;
  name: string;
  position: number;
  members: StatusMember[];
  // Le composant se lit, il n'est pas saisi : un par service d'un projet
  // publié qui n'a aucun composant.
  implicit: boolean;
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
  // Les objets visés, puis les composants que ces cibles touchent, tous
  // projets confondus : le serveur les calcule à la lecture.
  targets: { kind: MemberKind; id: string; name: string }[];
  components: { id: string; name: string }[];
  updates: IncidentUpdate[];
  created_at: string;
  updated_at: string;
  resolved_at: string | null;
}

export interface IncidentsResponse {
  incidents: Incident[];
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

// project_id ne compte qu'à la création : un composant ne change pas de projet.
export interface StatusComponentRequest {
  project_id: string;
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
  targets: { kind: MemberKind; id: string }[];
  starts_at: string | null;
  ends_at: string | null;
}

// Ce qui se corrige d'un incident : le titre, les cibles, la fenêtre.
export interface IncidentEditRequest {
  title: string;
  targets: { kind: MemberKind; id: string }[];
  starts_at: string | null;
  ends_at: string | null;
}

export interface IncidentUpdateRequest {
  status: IncidentStatus;
  message: string;
}

const statusPath = "/api/status";
const componentsPath = `${statusPath}/components`;
const incidentsPath = `${statusPath}/incidents`;

function componentPath(id: string): string {
  return `${componentsPath}/${segment(id)}`;
}

function incidentPath(id: string): string {
  return `${incidentsPath}/${segment(id)}`;
}

// Les composants d'un projet ; nul : ceux de tous les projets.
export function useStatusComponents(projectID: string | null): Resource<StatusComponentsResponse> {
  return useResource<StatusComponentsResponse>(projectID === null ? componentsPath : () => `${componentsPath}?project=${encodeURIComponent(projectID)}`);
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

// Sans id, une création ; avec, le remplacement de ce composant.
export async function saveStatusComponent(id: string | undefined, body: StatusComponentRequest): Promise<StatusComponent> {
  return id === undefined
    ? api.send<StatusComponent>(route("POST /api/status/components"), body)
    : api.send<StatusComponent>(route("PUT /api/status/components/{id}", { id }), body);
}

export async function deleteStatusComponent(id: string): Promise<void> {
  return api.send(route("DELETE /api/status/components/{id}", { id }));
}

export async function fetchComponentDeletePlan(id: string): Promise<DeletionPlan> {
  return api.get<DeletionPlan>(`${componentPath(id)}/delete-plan`);
}

export async function createIncident(body: IncidentRequest): Promise<Incident> {
  return api.send<Incident>(route("POST /api/status/incidents"), body);
}

export async function editIncident(id: string, body: IncidentEditRequest): Promise<Incident> {
  return api.send<Incident>(route("PUT /api/status/incidents/{id}", { id }), body);
}

// Une entrée du fil : un statut, un message, ou les deux.
export async function addIncidentUpdate(id: string, body: IncidentUpdateRequest): Promise<Incident> {
  return api.send<Incident>(route("POST /api/status/incidents/{id}/updates", { id }), body);
}

export async function deleteIncident(id: string): Promise<void> {
  return api.send(route("DELETE /api/status/incidents/{id}", { id }));
}
