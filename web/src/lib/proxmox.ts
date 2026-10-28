import type { ProxmoxGuest, ProxmoxStorage } from "../api/proxmox";
import type { Machine, MachineRole } from "../api/machines";
import type { Translate } from "../i18n/context";
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
