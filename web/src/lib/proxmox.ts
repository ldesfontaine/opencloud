import type { ActionState } from "../api/actions";
import type { GuestVerb, ProxmoxGuest, ProxmoxStorage } from "../api/proxmox";
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

// Les VM par numéro, les modèles à la suite : l'ordre dans lequel on les cherche.
export function sortedGuests(guests: ProxmoxGuest[]): ProxmoxGuest[] {
  return [...guests].sort((a, b) => Number(a.template) - Number(b.template) || a.vmid - b.vmid);
}

// Les stockages par nœud puis par nom.
export function sortedStorages(storages: ProxmoxStorage[]): ProxmoxStorage[] {
  return [...storages].sort((a, b) => a.node.localeCompare(b.node) || a.name.localeCompare(b.name));
}

// Les ressources allouées à une VM : « 2 vCPU · 2 Gio de mémoire ».
export function resourcesLabel(t: Translate, language: string, cpus: number, memory: number): string {
  return t("machine.proxmox_resources", formatDecimal(language, cpus, Number.isInteger(cpus) ? 0 : 1), formatBytes(t, language, memory));
}

// Une sauvegarde : « 2026-10-22 03:00 (233 Mio) », ou « Aucune ».
export function backupLabel(t: Translate, language: string, at: string | null, bytes: number, now: number): string {
  if (at === null) {
    return t("machine.proxmox_no_backup");
  }
  return t("machine.proxmox_backup_value", formatWhen(at, now), formatBytes(t, language, bytes));
}

// Une machine porte un hôte déclaré : l'onglet « Hyperviseur » s'affiche.
export function hostsProxmox(machine: Pick<Machine, "proxmox">): boolean {
  return machine.proxmox.declared;
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
      return t("machine.proxmox_blocked_offline");
    case "self":
      return t("machine.proxmox_blocked_self");
    case "right_missing":
      return t("machine.proxmox_blocked_right", block.right);
    case "right_unavailable":
      return describeUnavailable(t, block.reason, agent?.run_as ?? null);
  }
}

// Où en est un geste envoyé : parti, pris par l'agent, fait, ou échoué.
export type GestureStep = "sent" | "accepted" | "done" | "failed";

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
