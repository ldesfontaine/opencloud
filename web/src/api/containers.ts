import { useResource, type Resource } from "../hooks/useResource";
import { api, segment } from "./client";
import type { ContainerDirectory, UpdatePolicy } from "./directory";
import type { Lifecycle } from "./lifecycle";

// Les conteneurs vus par les agents, et leurs journaux. Sujet du direct :
// « containers » ; la fiche suit aussi « directory » (topicsOf).

// Un port publié sur l'hôte d'une machine.
export interface Port {
  ip: string;
  host_port: number;
  container_port: number;
  protocol: string;
}

// La présence d'un conteneur sur un réseau de sa machine ; l'adresse est
// vide quand le conteneur est arrêté.
interface Attachment {
  network_id: string;
  name: string;
  ip: string;
  aliases: string[];
}

// Une dépendance déclarée : par le label Compose ou par un lien hérité.
interface Dependency {
  name: string;
  source: "compose" | "link";
}

type FindingKind = "host_network" | "privileged" | "database_port_public" | "port_public";

// Un constat d'exposition, calculé par le serveur à la lecture : « warn »
// fait un point orange sur le nœud, « info » une ligne dans l'inspecteur.
export interface Finding {
  kind: FindingKind;
  level: "info" | "warn";
  port?: number;
  protocol?: string;
}

export type ContainerState = "created" | "running" | "paused" | "restarting" | "removing" | "exited" | "dead";
type ContainerHealth = "" | "starting" | "healthy" | "unhealthy";

// La mesure courante d'un conteneur, absente si elle n'est pas fraîche.
interface ContainerSample {
  sampled_at: string;
  cpu_percent: number;
  mem_used: number;
  mem_limit: number;
}

// Un conteneur : des faits ; la pastille se dérive dans lib/containers de
// l'état, du code de sortie et de la santé.
export interface Container {
  id: string;
  machine_id: string;
  machine_name: string;
  kind: "container";
  name: string;
  group: string;
  container_id: string;
  image: string;
  image_id: string;
  // Ce que Compose dit du conteneur : de quoi lire la commande fabriquée.
  compose_service: string;
  compose_dir: string;
  compose_file: string;
  // Lue sur l'unité de l'annuaire à laquelle la fiche est rattachée ; une
  // fiche non déclarée vaut « watch ».
  update_policy: UpdatePolicy;
  // Le service déclaré qui porte la fiche ; vide tant qu'elle n'est pas rattachée.
  declared_service_id: string;
  // Le dernier constat de l'agent sur l'image ; nul tant qu'il n'a rien dit.
  image_check: ImageCheck | null;
  // Le support de l'image : sur la fiche seulement, et seulement quand la
  // fin de support est activée sous Paramètres.
  lifecycle?: Lifecycle;
  state: ContainerState;
  // Faux : la machine ne signale plus depuis plus de deux minutes, « state »
  // est le dernier état connu, plus l'état actuel (lib/containers, displayedState).
  observed: boolean;
  // Le dernier signal de la machine, d'où se lit « dernier état, il y a » ;
  // nul tant que son agent n'en a donné aucun.
  machine_last_seen_at: string | null;
  exit_code: number;
  // Tué par le noyau faute de mémoire : 137 comme un docker stop, mais pas demandé.
  oom_killed: boolean;
  // Tué en SIGKILL sans arrêt demandé : 137 aussi, mais à signaler.
  killed: boolean;
  // Arrêt demandé avant la sortie : le code, même non nul, n'est pas un plantage.
  stop_requested: boolean;
  health: ContainerHealth;
  restart_count: number;
  ports: Port[];
  // bridge, host, none, container:<id>, ou le nom du premier réseau joint.
  network_mode: string;
  privileged: boolean;
  networks: Attachment[];
  depends_on: Dependency[];
  exposure: Finding[];
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
  first_seen_at: string;
  last_seen_at: string;
  archived_at: string | null;
  current: ContainerSample | null;
}

type UpdateKind = "" | "major" | "minor" | "patch" | "digest";

type CheckOutcome = "ok" | "local" | "unreachable" | "unauthorized" | "not_found" | "unsupported";

// Le constat de l'agent sur une image, et ce que le serveur en déduit :
// le type, l'image à tirer, la commande à copier, jamais exécutée.
export interface ImageCheck {
  checked_at: string;
  outcome: CheckOutcome;
  local_digest: string;
  remote_digest: string;
  newer_tag: string;
  newer_digest: string;
  kind: UpdateKind;
  target: string;
  command: string;
}

// Ce qu'une machine dit de son Docker.
export interface Engine {
  machine_id: string;
  present: boolean;
  reason: "" | "no_socket" | "denied" | "down" | "too_old";
  version: string;
  api_version: string;
  checked_at: string;
}

export interface ContainersResponse {
  containers: Container[];
  engines: Engine[];
}

export interface Transition {
  id: number;
  at: string;
  action: string;
  previous_state: ContainerState | "";
  new_state: ContainerState | "";
  previous_health: ContainerHealth;
  new_health: ContainerHealth;
  exit_code: number | null;
  oom_killed: boolean;
  killed: boolean;
  stop_requested: boolean;
  replayed: boolean;
  snippet: string;
}

export interface ContainerResponse {
  container: Container;
  engine: Engine | null;
  transitions: Transition[];
  // Ce que l'annuaire sait de ce conteneur ; nul pour une fiche qui n'en a pas.
  directory: ContainerDirectory | null;
}

export interface LogLine {
  at: string | null;
  stream: string;
  text: string;
}

interface LogsResponse {
  lines: LogLine[];
}

const containersPath = "/api/containers";

function containerPath(id: string): string {
  return `${containersPath}/${segment(id)}`;
}

// wanted à faux : rien n'est lu, pour une liste qui ne sert que sous un choix.
export function useContainers(wanted = true): Resource<ContainersResponse> {
  return useResource<ContainersResponse>(wanted ? containersPath : null);
}

// id nul : rien à lire (aucun conteneur d'origine).
export function useContainer(id: string | null): Resource<ContainerResponse> {
  return useResource<ContainerResponse>(id === null ? null : () => containerPath(id));
}

export function useMachineContainers(machineID: string): Resource<ContainersResponse> {
  return useResource<ContainersResponse>(() => `/api/machines/${segment(machineID)}/containers`);
}

// Les dernières lignes, d'un coup : le repli quand le flux ne tient pas.
export async function fetchContainerLogs(id: string, tail: number): Promise<LogsResponse> {
  return api.get<LogsResponse>(`${containerPath(id)}/logs?tail=${tail}`);
}

// Le flux SSE des journaux, que la page ouvre elle-même par EventSource.
// Lève comme segment() sur un identifiant qui n'en a pas la forme.
export function containerLogStreamPath(id: string, tail: number): string {
  return `${containerPath(id)}/logs/stream?tail=${tail}`;
}
