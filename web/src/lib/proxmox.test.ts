import { expect, test } from "vitest";

import type { Agent, Machine } from "../api/machines";
import type { ProxmoxGuest, ProxmoxStorage } from "../api/proxmox";
import type { Translate } from "../i18n/context";
import {
  backupLabel,
  detectedProxmox,
  gestureBlockText,
  gestureLabel,
  gestureQuestion,
  gestureRight,
  gestureStep,
  guestGestures,
  guestStatusLabel,
  guestTone,
  readErrorLabel,
  resourcesLabel,
  sortedGuests,
  sortedStorages,
  storagePercent,
  suggestedProxmoxURL,
} from "./proxmox";

// Un traducteur qui rend la clé et ses arguments : le test lit ce que la page demande.
const t: Translate = (key, ...args) => (args.length === 0 ? key : `${key}(${args.join("|")})`);

function guest(vmid: number, over: Partial<ProxmoxGuest> = {}): ProxmoxGuest {
  return { vmid, kind: "qemu", name: `vm${vmid}`, status: "running", template: false, node: "pve", cpus: 2, max_memory: 2 * 1024 ** 3, max_disk: 0, linked_machine_id: null, last_backup_at: null, last_backup_bytes: 0, own_snapshots: [], ...over };
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

test("l'hôte Proxmox se lit dans les rôles de la machine, et seulement là", () => {
  expect(detectedProxmox({ roles: [] })).toBeNull();
  expect(detectedProxmox({ roles: [{ name: "autre", version: "1" }] })).toBeNull();
  expect(detectedProxmox({ roles: [{ name: "proxmox_ve", version: "8.2.4" }] })).toEqual({ name: "proxmox_ve", version: "8.2.4" });
});

test("l'adresse proposée est celle de la machine sur le port 8006, IPv6 entre crochets", () => {
  expect(suggestedProxmoxURL({ address: "192.0.2.10" })).toBe("https://192.0.2.10:8006");
  expect(suggestedProxmoxURL({ address: "2001:db8::10" })).toBe("https://[2001:db8::10]:8006");
  expect(suggestedProxmoxURL({ address: "" })).toBe("");
});

function agent(over: Partial<Agent> = {}): Agent {
  return {
    id: "agt",
    version: "0.0.1",
    enrolled_at: "2026-10-01T00:00:00Z",
    revoked_at: null,
    capabilities: [
      { name: "observe", paths: [], unavailable: "" },
      { name: "proxmox.power", paths: [], unavailable: "" },
      { name: "proxmox.snapshot", paths: [], unavailable: "" },
    ],
    reported_at: "2026-10-01T00:00:00Z",
    requested: [],
    capabilities_differ: false,
    server_key_fingerprint: "",
    run_as: { uid: 998, name: "opencloud" },
    rollback_capable: true,
    ...over,
  };
}

function host(over: Partial<Pick<Machine, "agent" | "online">> = {}): Pick<Machine, "agent" | "online"> {
  return { agent: agent(), online: true, ...over };
}

const verbsOf = (gestures: { verb: string; snapshot: string }[]) => gestures.map((gesture) => (gesture.snapshot === "" ? gesture.verb : `${gesture.verb}:${gesture.snapshot}`));

test("les gestes suivent l'état : démarrer à l'arrêt, arrêter en marche, l'instantané toujours, ses propres instantanés à supprimer", () => {
  expect(verbsOf(guestGestures(guest(100, { status: "stopped" }), host()))).toEqual(["start", "snapshot"]);
  expect(verbsOf(guestGestures(guest(100, { status: "running", own_snapshots: ["oc-20261028-101500"] }), host()))).toEqual([
    "shutdown",
    "snapshot",
    "snapshot-delete:oc-20261028-101500",
  ]);
  expect(verbsOf(guestGestures(guest(100, { status: "paused" }), host()))).toEqual(["snapshot"]);
  expect(guestGestures(guest(100, { status: "running" }), host()).every((gesture) => gesture.blocked === null)).toBe(true);
});

test("un modèle n'a aucun geste", () => {
  expect(guestGestures(guest(9000, { template: true, status: "stopped" }), host())).toEqual([]);
});

test("le droit d'un geste : power pour démarrer et arrêter, snapshot pour les instantanés", () => {
  expect(gestureRight("start")).toBe("proxmox.power");
  expect(gestureRight("shutdown")).toBe("proxmox.power");
  expect(gestureRight("snapshot")).toBe("proxmox.snapshot");
  expect(gestureRight("snapshot-delete")).toBe("proxmox.snapshot");
});

test("un droit que l'agent n'a pas rapporté grise le geste, et la raison le nomme", () => {
  const onlyPower = host({ agent: agent({ capabilities: [{ name: "observe", paths: [], unavailable: "" }, { name: "proxmox.power", paths: [], unavailable: "" }] }) });
  const [start, snapshot] = guestGestures(guest(100, { status: "stopped" }), onlyPower);
  expect(start?.blocked).toBeNull();
  expect(snapshot?.blocked).toEqual({ kind: "right_missing", right: "proxmox.snapshot" });
  expect(gestureBlockText(t, { kind: "right_missing", right: "proxmox.snapshot" }, null)).toBe("machine.proxmox_blocked_right(proxmox.snapshot)");
});

test("un droit rapporté mais impossible à exercer grise le geste avec la phrase de la raison", () => {
  const unsafe = host({ agent: agent({ capabilities: [{ name: "proxmox.power", paths: [], unavailable: "proxmox_token_unsafe" }] }) });
  const [start] = guestGestures(guest(100, { status: "stopped" }), unsafe);
  expect(start?.blocked).toEqual({ kind: "right_unavailable", reason: "proxmox_token_unsafe" });
  expect(gestureBlockText(t, { kind: "right_unavailable", reason: "proxmox_token_unsafe" }, null)).toBe("machine.capability_unavailable_proxmox_token_unsafe");
});

test("tant que l'agent n'a rien rapporté de ses droits, rien n'est grisé pour un droit absent", () => {
  const silent = host({ agent: agent({ reported_at: null, capabilities: [] }) });
  expect(guestGestures(guest(100, { status: "stopped" }), silent).every((gesture) => gesture.blocked === null)).toBe(true);
});

test("sans agent actif, ou hors ligne, tout est grisé", () => {
  expect(guestGestures(guest(100), host({ agent: null }))[0]?.blocked).toEqual({ kind: "no_agent" });
  expect(guestGestures(guest(100), host({ agent: agent({ revoked_at: "2026-10-02T00:00:00Z" }) }))[0]?.blocked).toEqual({ kind: "no_agent" });
  expect(guestGestures(guest(100), host({ online: false }))[0]?.blocked).toEqual({ kind: "offline" });
});

test("la VM qui héberge openCloud ne s'arrête pas d'ici ; son instantané reste possible", () => {
  const [shutdown, snapshot] = guestGestures(guest(100, { status: "running", linked_machine_id: "local" }), host());
  expect(shutdown?.blocked).toEqual({ kind: "self" });
  expect(snapshot?.blocked).toBeNull();
  expect(gestureBlockText(t, { kind: "self" }, null)).toBe("machine.proxmox_blocked_self");
});

test("libellés et questions disent le geste, l'invité et l'instantané", () => {
  expect(gestureLabel(t, { verb: "shutdown", snapshot: "" })).toBe("machine.proxmox_gesture_shutdown");
  expect(gestureLabel(t, { verb: "snapshot-delete", snapshot: "oc-1" })).toBe("machine.proxmox_gesture_snapshot_delete(oc-1)");
  expect(gestureQuestion(t, { verb: "start", snapshot: "" }, "dns")).toBe("machine.proxmox_confirm_start(dns)");
  expect(gestureQuestion(t, { verb: "snapshot-delete", snapshot: "oc-1" }, "dns")).toBe("machine.proxmox_confirm_snapshot_delete(oc-1|dns)");
});

test("l'avancement d'un geste : envoyé, accepté par l'agent, fait, ou échoué", () => {
  expect(gestureStep("queued")).toBe("sent");
  expect(gestureStep("sent")).toBe("sent");
  expect(gestureStep("accepted")).toBe("accepted");
  expect(gestureStep("succeeded")).toBe("done");
  expect(gestureStep("refused")).toBe("failed");
  expect(gestureStep("failed")).toBe("failed");
  expect(gestureStep("expired")).toBe("failed");
});
