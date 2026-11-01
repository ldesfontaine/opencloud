import type { ActionState } from "../api/actions";
import type { GuestVerb, ProxmoxGuest, ProxmoxHost, ProxmoxStorage } from "../api/proxmox";
import type { Machine, MachineRole } from "../api/machines";
import type { Translate } from "../i18n/context";
import { describeUnavailable } from "./machines";
import type { Tone } from "./tone";
import { formatWhen } from "./time";
import { formatBytes, formatDecimal, percentOf } from "./units";

// Le ton de la pastille d'état d'une VM : en marche va, le reste est neutre.
export function guestTone(status: string): Tone {
  return status === "running" ? "ok" : "neutral";
}

// L'état d'une VM dans la langue de l'opérateur ; un état que le catalogue ne
// connaît pas devient « Autre ».
export function guestStatusLabel(t: Translate, status: string): string {
  switch (status) {
    case "running":
      return t("machine.proxmox_status_running");
    case "stopped":
      return t("machine.proxmox_status_stopped");
    case "paused":
      return t("machine.proxmox_status_paused");
    default:
      return t("machine.proxmox_status_other");
  }
}

export function kindLabel(t: Translate, kind: string): string {
  return kind === "lxc" ? t("machine.proxmox_kind_lxc") : t("machine.proxmox_kind_qemu");
}

// La phrase du dernier relevé qui a échoué : la clé du refus, traduite. Une
// clé que le front ne connaît pas montre la réponse inattendue.
export function readErrorLabel(t: Translate, key: string): string {
  switch (key) {
    case "proxmox.unreachable":
      return t("proxmox.unreachable");
    case "proxmox.certificate":
      return t("proxmox.certificate");
    case "proxmox.unauthorized":
      return t("proxmox.unauthorized");
    default:
      return t("proxmox.bad_response");
  }
}

// La part utilisée d'un stockage, en pour cent entier.
export function storagePercent(storage: Pick<ProxmoxStorage, "used" | "total">): number {
  return percentOf(storage.used, storage.total);
}

// Les stockages par nœud puis par nom.
export function sortedStorages(storages: ProxmoxStorage[]): ProxmoxStorage[] {
  return [...storages].sort((a, b) => a.node.localeCompare(b.node) || a.name.localeCompare(b.name));
}

// Les ressources allouées à une VM : « 2 vCPU · 2 Gio de mémoire ».
export function resourcesLabel(t: Translate, language: string, cpus: number, memory: number): string {
  return t("machine.proxmox_resources", formatDecimal(language, cpus, Number.isInteger(cpus) ? 0 : 1), formatBytes(t, language, memory));
}

// Ce que l'on sait de la dernière sauvegarde d'un invité. « unknown » n'est pas
// « none » : l'hôte n'a pas pu lister les archives faute de droit, il ne dit pas
// qu'il n'y en a pas.
interface BackupFacts {
  at: string | null;
  bytes: number;
  unknown: boolean;
}

// Au-delà de deux jours une sauvegarde de chaque nuit manque une fois ; au-delà
// d'une semaine, elle est perdue de vue.
const backupFreshHours = 48;
const backupStaleHours = 7 * 24;

// Une sauvegarde : « 2026-10-22 03:00 (233 Mio) », « Aucune », ou « Inconnue ».
export function backupLabel(t: Translate, language: string, facts: BackupFacts, now: number): string {
  if (facts.unknown) {
    return t("machine.proxmox_backup_unknown");
  }
  if (facts.at === null) {
    return t("machine.proxmox_no_backup");
  }
  return t("machine.proxmox_backup_value", formatWhen(facts.at, now), formatBytes(t, language, facts.bytes));
}

// Le ton de la pastille d'une sauvegarde : récente va, ancienne demande
// attention, perdue de vue est en danger ; aucune demande attention ; inconnue
// reste neutre (DA : un état inconnu ne se dessine ni en vert ni en rouge).
export function backupTone(facts: BackupFacts, now: number): Tone {
  if (facts.unknown) {
    return "neutral";
  }
  if (facts.at === null) {
    return "warn";
  }
  const hours = (now - Date.parse(facts.at)) / 3_600_000;
  if (hours <= backupFreshHours) {
    return "ok";
  }
  return hours <= backupStaleHours ? "warn" : "danger";
}

// Les invités rangés comme on les cherche : ceux qui tournent (ou en pause),
// ceux qui sont arrêtés, puis les modèles à part. Chaque rangée garde l'ordre
// des numéros.
export interface GuestGroups {
  live: ProxmoxGuest[];
  stopped: ProxmoxGuest[];
  templates: ProxmoxGuest[];
}

export function groupGuests(guests: ProxmoxGuest[]): GuestGroups {
  const sorted = [...guests].sort((a, b) => a.vmid - b.vmid);
  return {
    templates: sorted.filter((guest) => guest.template),
    stopped: sorted.filter((guest) => !guest.template && guest.status === "stopped"),
    live: sorted.filter((guest) => !guest.template && guest.status !== "stopped"),
  };
}

// Ce qu'une VM alloue, en part de la plus grosse de l'hôte : l'hôte ne dit pas
// ce que la VM consomme, ni la capacité de son nœud, seulement ce qu'il lui a
// alloué. La jauge compare les VM entre elles, elle ne mesure pas une charge.
export interface AllocationShares {
  cpus: number;
  memory: number;
  disk: number;
}

export function allocationShares(guest: ProxmoxGuest, among: readonly ProxmoxGuest[]): AllocationShares {
  const peers = among.filter((peer) => !peer.template);
  const share = (value: number, pick: (peer: ProxmoxGuest) => number) => percentOf(value, Math.max(...peers.map(pick), 0));
  return {
    cpus: share(guest.cpus, (peer) => peer.cpus),
    memory: share(guest.max_memory, (peer) => peer.max_memory),
    disk: share(guest.max_disk, (peer) => peer.max_disk),
  };
}

// Un stockage reçoit-il les sauvegardes, et le jeton les voit-il ?
export type StorageBackupState = "none" | "readable" | "blind";

export function storageBackupState(storage: ProxmoxStorage, blind: readonly string[]): StorageBackupState {
  if (!storage.content.includes("backup")) {
    return "none";
  }
  return blind.includes(storage.name) ? "blind" : "readable";
}

// Le port de l'API d'un hôte Proxmox VE.
const proxmoxAPIPort = 8006;

// Le rôle « hôte Proxmox VE » que l'agent a reconnu sur la machine, null s'il
// ne l'a pas vu.
export function detectedProxmox(machine: Pick<Machine, "roles">): MachineRole | null {
  return machine.roles.find((role) => role.name === "proxmox_ve") ?? null;
}

// L'adresse que le formulaire propose pour l'hôte : celle de la machine, sur le
// port de l'API. Vide quand la machine n'a pas d'adresse. Une adresse IPv6 se
// met entre crochets.
export function suggestedProxmoxURL(machine: Pick<Machine, "address">): string {
  if (machine.address === "") {
    return "";
  }
  const host = machine.address.includes(":") ? `[${machine.address}]` : machine.address;
  return `https://${host}:${proxmoxAPIPort}`;
}

// Ce que le formulaire de l'hôte Proxmox tient.
export interface ProxmoxFormValues {
  apiURL: string;
  tokenID: string;
  tokenSecret: string;
  pin: string;
}

// Un hôte à déclarer : rien, que l'adresse proposée quand l'agent a reconnu
// Proxmox (le jeton, lui, reste à l'opérateur).
export function emptyProxmoxForm(suggestedURL: string): ProxmoxFormValues {
  return { apiURL: suggestedURL, tokenID: "", tokenSecret: "", pin: "" };
}

// Ce que le serveur garde d'un hôte déjà déclaré, pour le modifier : jamais le
// secret, ni l'identifiant du jeton en entier ; ils se ressaisissent ensemble.
export function proxmoxFormOf(host: Pick<ProxmoxHost, "api_url" | "pin">): ProxmoxFormValues {
  return { apiURL: host.api_url, tokenID: "", tokenSecret: "", pin: host.pin };
}

// La saisie a changé depuis le formulaire d'origine : fermer la fenêtre demande
// alors s'il faut l'abandonner.
export function isProxmoxFormModified(values: ProxmoxFormValues, initial: ProxmoxFormValues): boolean {
  return (Object.keys(initial) as (keyof ProxmoxFormValues)[]).some((key) => values[key].trim() !== initial[key].trim());
}

// L'étape « Accès » demande l'adresse, l'identifiant et le secret du jeton.
export function proxmoxAccessMissing(values: ProxmoxFormValues): boolean {
  return values.apiURL.trim() === "" || values.tokenID.trim() === "" || values.tokenSecret === "";
}

// Le corps envoyé au serveur, nettoyé des espaces de saisie.
export function proxmoxDefinitionOf(values: ProxmoxFormValues): { api_url: string; token_id: string; token_secret: string; pin: string } {
  return { api_url: values.apiURL.trim(), token_id: values.tokenID.trim(), token_secret: values.tokenSecret, pin: values.pin.trim() };
}

// L'identifiant de la machine openCloud elle-même (machine.LocalID côté Go).
const localMachineID = "local";

// Pourquoi un geste ne peut pas partir : il reste au menu, grisé, avec sa raison.
export type GestureBlock =
  | { kind: "no_agent" }
  | { kind: "offline" }
  | { kind: "self" }
  | { kind: "right_missing"; right: string }
  | { kind: "right_unavailable"; reason: string };

// Un geste que le menu d'un invité propose ; snapshot ne sert qu'à la
// suppression d'un instantané.
export interface GuestGesture {
  verb: GuestVerb;
  snapshot: string;
  blocked: GestureBlock | null;
}

// L'hôte tel que les gestes le jugent : la machine qui le porte et son agent.
type GestureHost = Pick<Machine, "agent" | "online">;

// Les gestes d'un invité selon son état : démarrer un invité arrêté, arrêter
// proprement un invité en marche, prendre un instantané de tout invité, en
// supprimer un des siens. Un modèle n'en a aucun. Chacun porte la raison qui
// le grise, s'il y en a une ; le serveur et l'agent jugent encore.
export function guestGestures(guest: ProxmoxGuest, host: GestureHost): GuestGesture[] {
  if (guest.template) {
    return [];
  }
  const verbs: { verb: GuestVerb; snapshot: string }[] = [];
  if (guest.status === "stopped") {
    verbs.push({ verb: "start", snapshot: "" });
  }
  if (guest.status === "running") {
    verbs.push({ verb: "shutdown", snapshot: "" });
  }
  verbs.push({ verb: "snapshot", snapshot: "" });
  for (const snapshot of guest.own_snapshots) {
    verbs.push({ verb: "snapshot-delete", snapshot });
  }
  return verbs.map((gesture) => ({ ...gesture, blocked: gestureBlock(gesture.verb, guest, host) }));
}

// Le droit que l'agent de l'hôte doit avoir pour un geste.
export function gestureRight(verb: GuestVerb): string {
  return verb === "start" || verb === "shutdown" ? "proxmox.power" : "proxmox.snapshot";
}

function gestureBlock(verb: GuestVerb, guest: ProxmoxGuest, host: GestureHost): GestureBlock | null {
  // La VM qui héberge openCloud ne s'arrête jamais d'ici : le serveur le refuse aussi.
  if (verb === "shutdown" && guest.linked_machine_id === localMachineID) {
    return { kind: "self" };
  }
  const agent = host.agent;
  if (agent === null || agent.revoked_at !== null) {
    return { kind: "no_agent" };
  }
  if (!host.online) {
    return { kind: "offline" };
  }
  // Tant que l'agent n'a rien rapporté de ses droits, on ne devine pas : le
  // serveur et l'agent jugeront.
  if (agent.reported_at === null || agent.capabilities.length === 0) {
    return null;
  }
  const right = gestureRight(verb);
  const reported = agent.capabilities.find((capability) => capability.name === right);
  if (reported === undefined) {
    return { kind: "right_missing", right };
  }
  if (reported.unavailable !== "") {
    return { kind: "right_unavailable", reason: reported.unavailable };
  }
  return null;
}

// Le libellé d'un geste au menu.
export function gestureLabel(t: Translate, gesture: Pick<GuestGesture, "verb" | "snapshot">): string {
  switch (gesture.verb) {
    case "start":
      return t("machine.proxmox_gesture_start");
    case "shutdown":
      return t("machine.proxmox_gesture_shutdown");
    case "snapshot":
      return t("machine.proxmox_gesture_snapshot");
    case "snapshot-delete":
      return t("machine.proxmox_gesture_snapshot_delete", gesture.snapshot);
  }
}

// La question de la confirmation : le geste et sa conséquence.
export function gestureQuestion(t: Translate, gesture: Pick<GuestGesture, "verb" | "snapshot">, guestName: string): string {
  switch (gesture.verb) {
    case "start":
      return t("machine.proxmox_confirm_start", guestName);
    case "shutdown":
      return t("machine.proxmox_confirm_shutdown", guestName);
    case "snapshot":
      return t("machine.proxmox_confirm_snapshot", guestName);
    case "snapshot-delete":
      return t("machine.proxmox_confirm_snapshot_delete", gesture.snapshot, guestName);
  }
}

// La raison qui grise un geste, en une phrase.
export function gestureBlockText(t: Translate, block: GestureBlock, agent: Pick<NonNullable<Machine["agent"]>, "run_as"> | null): string {
  switch (block.kind) {
    case "no_agent":
      return t("machine.proxmox_blocked_no_agent");
    case "offline":
      return t("agent.offline_reason");
    case "self":
      return t("machine.proxmox_blocked_self");
    case "right_missing":
      return t("machine.proxmox_blocked_right", block.right);
    case "right_unavailable":
      return describeUnavailable(t, block.reason, agent?.run_as ?? null);
  }
}

// Où en est un geste envoyé : parti, pris par l'agent, fait, ou échoué.
type GestureStep = "sent" | "accepted" | "done" | "failed";

export function gestureStep(state: ActionState): GestureStep {
  switch (state) {
    case "queued":
    case "sent":
      return "sent";
    case "accepted":
      return "accepted";
    case "succeeded":
      return "done";
    default:
      return "failed";
  }
}

export const gestureStepTones: Record<GestureStep, Tone> = {
  sent: "neutral",
  accepted: "accent",
  done: "ok",
  failed: "danger",
};
