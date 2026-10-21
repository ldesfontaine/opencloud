import { useResource, type Resource } from "../hooks/useResource";
import { api, segment } from "./client";
import type { DeletionPlan } from "./deletion";

// Les pages de statut : chacune ses réglages et ses composants, les
// incidents qui les touchent, côté opérateur, et ce que le public lit
// (statut/, qui a ses propres chemins). Sujet du direct : « status », que
// topicsOf devine du chemin.

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
  page_id: string;
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

// Une page de statut, à son adresse /statut/<slug> ; project_id vide : la
// page n'est rangée dans aucun projet. La langue est résolue par le serveur.
export interface StatusPage {
  id: string;
  project_id: string;
  slug: string;
  title: string;
  announcement: string;
  language: string;
  public_url: string;
  created_at: string;
  updated_at: string;
}

export interface StatusPagesResponse {
  pages: StatusPage[];
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

// page_id ne compte qu'à la création : un composant ne change pas de page.
export interface StatusComponentRequest {
  page_id: string;
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
  slug: string;
  title: string;
  announcement: string;
  language: string;
  project_id: string;
}

const statusPath = "/api/status";
const pagesPath = `${statusPath}/pages`;
const componentsPath = `${statusPath}/components`;
const incidentsPath = `${statusPath}/incidents`;

function pagePath(id: string): string {
  return `${pagesPath}/${segment(id)}`;
}

function componentPath(id: string): string {
  return `${componentsPath}/${segment(id)}`;
}

function incidentPath(id: string): string {
  return `${incidentsPath}/${segment(id)}`;
}

export function useStatusPages(): Resource<StatusPagesResponse> {
  return useResource<StatusPagesResponse>(pagesPath);
}

// id nul : rien à lire (un formulaire de création).
export function useStatusPage(id: string | null): Resource<StatusPage> {
  return useResource<StatusPage>(id === null ? null : () => pagePath(id));
}

// Les composants d'une page ; nul : ceux de toutes les pages (le choix des
// composants d'un incident).
export function useStatusComponents(pageID: string | null): Resource<StatusComponentsResponse> {
  return useResource<StatusComponentsResponse>(pageID === null ? componentsPath : () => `${componentsPath}?page=${encodeURIComponent(pageID)}`);
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
  return id === undefined ? api.post<StatusComponent>(componentsPath, body) : api.put<StatusComponent>(componentPath(id), body);
}

export async function deleteStatusComponent(id: string): Promise<void> {
  return api.delete(componentPath(id));
}

export async function fetchComponentDeletePlan(id: string): Promise<DeletionPlan> {
  return api.get<DeletionPlan>(`${componentPath(id)}/delete-plan`);
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

// Sans id, une création ; avec, le remplacement de cette page.
export async function saveStatusPage(id: string | undefined, body: StatusPageRequest): Promise<StatusPage> {
  return id === undefined ? api.post<StatusPage>(pagesPath, body) : api.put<StatusPage>(pagePath(id), body);
}

export async function deleteStatusPage(id: string): Promise<void> {
  return api.delete(pagePath(id));
}

export async function fetchStatusPageDeletePlan(id: string): Promise<DeletionPlan> {
  return api.get<DeletionPlan>(`${pagePath(id)}/delete-plan`);
}
