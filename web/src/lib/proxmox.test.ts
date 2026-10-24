import { expect, test } from "vitest";

import type { ProxmoxGuest, ProxmoxStorage } from "../api/proxmox";
import type { Translate } from "../i18n/context";
import { backupLabel, guestStatusLabel, guestTone, readErrorLabel, resourcesLabel, sortedGuests, sortedStorages, storagePercent } from "./proxmox";

// Un traducteur qui rend la clé et ses arguments : le test lit ce que la page demande.
const t: Translate = (key, ...args) => (args.length === 0 ? key : `${key}(${args.join("|")})`);

function guest(vmid: number, over: Partial<ProxmoxGuest> = {}): ProxmoxGuest {
  return { vmid, kind: "qemu", name: `vm${vmid}`, status: "running", template: false, node: "pve", cpus: 2, max_memory: 2 * 1024 ** 3, max_disk: 0, linked_machine_id: null, last_backup_at: null, last_backup_bytes: 0, ...over };
}

function storage(name: string, node: string, over: Partial<ProxmoxStorage> = {}): ProxmoxStorage {
  return { node, name, type: "dir", total: 100, used: 40, avail: 60, content: ["backup"], active: true, ...over };
}

test("une VM en marche est ok, les autres états neutres, et un état inconnu s'écrit « autre »", () => {
  expect(guestTone("running")).toBe("ok");
  expect(guestTone("stopped")).toBe("neutral");
  expect(guestStatusLabel(t, "running")).toBe("machine.proxmox_status_running");
  expect(guestStatusLabel(t, "paused")).toBe("machine.proxmox_status_paused");
  expect(guestStatusLabel(t, "prelaunch")).toBe("machine.proxmox_status_other");
});

test("la raison du dernier relevé est une phrase du catalogue, la réponse inattendue par défaut", () => {
  expect(readErrorLabel(t, "proxmox.unreachable")).toBe("proxmox.unreachable");
  expect(readErrorLabel(t, "proxmox.unauthorized")).toBe("proxmox.unauthorized");
  expect(readErrorLabel(t, "proxmox.certificate")).toBe("proxmox.certificate");
  expect(readErrorLabel(t, "autre")).toBe("proxmox.bad_response");
});

test("les VM se rangent par numéro, les modèles à la suite", () => {
  const ordered = sortedGuests([guest(9000, { template: true }), guest(300), guest(100)]);
  expect(ordered.map((entry) => entry.vmid)).toEqual([100, 300, 9000]);
  expect(sortedGuests([guest(5), guest(2, { template: true }), guest(1)]).map((entry) => entry.vmid)).toEqual([1, 5, 2]);
});

test("les stockages se rangent par nœud puis par nom", () => {
  const ordered = sortedStorages([storage("local", "pve2"), storage("zfs", "pve"), storage("local", "pve")]);
  expect(ordered.map((entry) => `${entry.node}/${entry.name}`)).toEqual(["pve/local", "pve/zfs", "pve2/local"]);
});

test("l'usage d'un stockage est un pour cent entier, nul quand il n'a pas de taille", () => {
  expect(storagePercent({ used: 40, total: 100 })).toBe(40);
  expect(storagePercent({ used: 0, total: 0 })).toBe(0);
  expect(storagePercent({ used: 200, total: 100 })).toBe(100);
});

test("une sauvegarde dit sa date et sa taille, ou qu'il n'y en a aucune", () => {
  expect(backupLabel(t, "fr", null, 0, 0)).toBe("machine.proxmox_no_backup");
  expect(backupLabel(t, "fr", "2026-10-01T03:00:00Z", 233 * 1024 ** 2, Date.parse("2026-10-24T12:00:00Z"))).toContain("machine.proxmox_backup_value(");
});

test("les ressources allouées entiers sans décimale, fractionnaires avec une", () => {
  expect(resourcesLabel(t, "fr", 2, 2 * 1024 ** 3)).toContain("machine.proxmox_resources(2|");
  expect(resourcesLabel(t, "fr", 0.5, 1024 ** 3)).toContain("0,5");
});
