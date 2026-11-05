import { useResource, type Resource } from "../hooks/useResource";
import { api, segment } from "./client";
import { route } from "./routes";

// Les alertes, les canaux qui les portent, et les silences qui les taisent
// (« mutes » côté serveur). Sujet du direct : « alerts », que topicsOf
// devine du chemin.

export type AlertKind =
  | "machine_lost"
  | "service_down"
  | "service_unhealthy"
  | "service_restart"
  | "job_late"
  | "job_failed"
  | "probe_down"
  | "cert_expiring"
  | "cert_expired"
  | "cert_untrusted"
  | "disk_full"
  | "cpu_high"
  | "memory_high"
  | "agent_rolled_back"
  | "backup_stale"
  | "restore_test_stale"
  | "source_unprotected"
  | "target_full"
  | "target_unreachable";

// Le catalogue fermé, dans l'ordre des écrans.
export const alertKinds: readonly AlertKind[] = [
  "machine_lost",
  "service_down",
  "service_unhealthy",
  "service_restart",
  "job_late",
  "job_failed",
  "probe_down",
  "cert_expiring",
  "cert_expired",
  "cert_untrusted",
  "disk_full",
  "cpu_high",
  "memory_high",
  "agent_rolled_back",
  "backup_stale",
  "restore_test_stale",
  "source_unprotected",
  "target_full",
  "target_unreachable",
];

export type AlertSeverity = "attention" | "danger";
type AlertStatus = "open" | "resolved";
export type AlertObjectKind = "machine" | "container" | "heartbeat" | "probe" | "volume" | "backup";

export interface AlertObject {
  kind: AlertObjectKind;
  id: string;
  name: string;
}

// Les alertes ouvertes, et celles que personne n'a lues.
export interface AlertCounts {
  open: number;
  unacknowledged: number;
}

// Les chiffres du fait ; la phrase se fait dans lib/alerts avec les mêmes
// clés que le canal.
interface AlertDetails {
  count?: number;
  percent?: number;
  // La fenêtre, en minutes, de la moyenne d'un processeur ou d'une mémoire.
  window_minutes?: number;
  mount_point?: string;
  // Le seuil d'attention en vigueur à l'ouverture : la phrase cite celui-là.
  threshold?: number;
  exit_code?: number;
  reason?: string;
  target?: string;
  not_after?: string;
  since?: string;
  // La place libre d'une cible de sauvegardes.
  bytes?: number;
}

export interface Alert {
  id: number;
  kind: AlertKind;
  severity: AlertSeverity;
  status: AlertStatus;
  // Un silence la couvre en ce moment : rien n'en part.
  muted: boolean;
  object: AlertObject;
  machine_id: string;
  machine_name: string;
  // Le service déclaré du conteneur ou de la sonde ; vide sinon.
  service_id: string;
  details: AlertDetails;
  opened_at: string;
  updated_at: string;
  resolved_at: string | null;
  acknowledged_at: string | null;
}

export interface AlertsResponse {
  alerts: Alert[];
  counts: AlertCounts;
}

type DeliveryEvent = "opened" | "aggravated" | "reminded" | "resolved";
type DeliveryStatus = "pending" | "delivered" | "failed" | "held" | "muted";

export interface Delivery {
  id: number;
  channel_id: number;
  event: DeliveryEvent;
  status: DeliveryStatus;
  attempts: number;
  reason: string;
  code: number;
  updated_at: string;
  // L'essai suivant d'une livraison en échec ; null : plus d'essai.
  next_attempt_at: string | null;
}

export interface AlertResponse {
  alert: Alert;
  deliveries: Delivery[];
}

export type ChannelFormat = "json" | "text" | "discord" | "slack" | "email" | "matrix";

export interface Channel {
  id: number;
  name: string;
  url: string;
  format: ChannelFormat;
  // Où le message arrive quand l'URL ne le dit pas ; absent pour un webhook.
  destination?: string;
  has_secret: boolean;
  min_severity: AlertSeverity;
  notify_resolve: boolean;
  // Le canal reçoit aussi le journal d'audit.
  receives_audit: boolean;
  enabled: boolean;
  // L'URL est en http : le canal parle en clair.
  plain: boolean;
  created_at: string;
}

export interface ChannelsResponse {
  channels: Channel[];
}

// Le résultat d'un essai d'envoi : celui d'un canal, ou celui du relais
// e-mail sous Paramètres.
export interface ChannelTest {
  ok: boolean;
  reason: string;
  code: number;
}

// Ce qu'un silence tait ; vide : tout.
export type MuteTargetKind = "" | "machine" | "service" | "container" | "probe" | "heartbeat";

export interface Mute {
  id: number;
  kind: AlertKind | "";
  target: { kind: MuteTargetKind; id: string; name: string };
  reason: string;
  starts_at: string;
  ends_at: string;
  active: boolean;
  // L'acteur du journal d'audit ; vide pour un silence venu d'avant.
  created_by_kind: string;
  created_by_name: string;
  created_at: string;
  // Annoncé, le silence se montre en maintenance sur les pages de statut,
  // sous ce titre public ; sinon il reste interne.
  announce: boolean;
  public_title: string;
}

export interface MutesResponse {
  mutes: Mute[];
}

// Le corps de la création comme de l'édition d'un canal. Le secret vide
// garde l'ancien ; clear_secret l'efface.
export interface ChannelRequest {
  name: string;
  url: string;
  format: ChannelFormat;
  destination: string;
  secret: string;
  clear_secret: boolean;
  min_severity: AlertSeverity;
  notify_resolve: boolean;
  receives_audit: boolean;
  enabled: boolean;
}

// Un silence vise un type d'alerte, un objet, les deux, ou tout ; sans
// début, il commence tout de suite.
export interface MuteRequest {
  kind: AlertKind | "";
  target_kind: MuteTargetKind;
  target_id: string;
  reason: string;
  starts_at: string | null;
  duration_minutes: number;
  announce: boolean;
  public_title: string;
}

// Les identifiants d'alerte, de canal et de silence sont des entiers ; une
// route les donne en texte.
type NumericID = number | string;

const alertsPath = "/api/alerts";
const channelsPath = `${alertsPath}/channels`;
const mutesPath = `${alertsPath}/mutes`;

function alertPath(id: NumericID): string {
  return `${alertsPath}/${segment(String(id))}`;
}

function channelPath(id: NumericID): string {
  return `${channelsPath}/${segment(String(id))}`;
}

// Les ouvertes (lues ou non) par défaut, ou les résolues.
export function useAlerts(status: AlertStatus = "open"): Resource<AlertsResponse> {
  return useResource<AlertsResponse>(status === "resolved" ? `${alertsPath}?status=resolved` : alertsPath);
}

export function useAlert(id: string): Resource<AlertResponse> {
  return useResource<AlertResponse>(() => alertPath(id));
}

export function useChannels(): Resource<ChannelsResponse> {
  return useResource<ChannelsResponse>(channelsPath);
}

// id nul : rien à lire (un formulaire de création).
export function useChannel(id: string | null): Resource<Channel> {
  return useResource<Channel>(id === null ? null : () => channelPath(id));
}

export function useMutes(): Resource<MutesResponse> {
  return useResource<MutesResponse>(mutesPath);
}

// Marque des alertes comme lues d'un coup ; la réponse dit combien l'étaient vraiment (le verbe d'API reste « acknowledge »).
export async function acknowledgeAlerts(ids: number[]): Promise<{ acknowledged: number }> {
  return api.send<{ acknowledged: number }>(route("POST /api/alerts/actions/acknowledge"), { ids });
}

export async function acknowledgeAlert(id: NumericID): Promise<void> {
  return api.send(route("POST /api/alerts/{id}/actions/acknowledge", { id: String(id) }));
}

// Sans id, une création ; avec, le remplacement de ce canal.
export async function saveChannel(id: NumericID | undefined, body: ChannelRequest): Promise<Channel> {
  return id === undefined ? api.send<Channel>(route("POST /api/alerts/channels"), body) : api.send<Channel>(route("PUT /api/alerts/channels/{id}", { id: String(id) }), body);
}

export async function deleteChannel(id: NumericID): Promise<void> {
  return api.send(route("DELETE /api/alerts/channels/{id}", { id: String(id) }));
}

// Un vrai envoi, dont le résultat revient tel quel.
export async function testChannel(id: NumericID): Promise<ChannelTest> {
  return api.send<ChannelTest>(route("POST /api/alerts/channels/{id}/actions/test", { id: String(id) }));
}

export async function createMute(body: MuteRequest): Promise<Mute> {
  return api.send<Mute>(route("POST /api/alerts/mutes"), body);
}

export async function deleteMute(id: NumericID): Promise<void> {
  return api.send(route("DELETE /api/alerts/mutes/{id}", { id: String(id) }));
}
