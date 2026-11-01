import { expect, test } from "vitest";

import type { Machine, MachineHost } from "../api/machines";
import type { ProxmoxCluster, ProxmoxMember } from "../api/proxmox";
import { buildMachineTree, summarizeMachines, type TreeRow } from "./machineTree";

function machine(id: string, over: Partial<Machine> = {}): Machine {
  return {
    id,
    name: id,
    hostname: id,
    address: "",
    os: "Debian 12",
    arch: "amd64",
    online: true,
    update_last: false,
    last_seen_at: null,
    agent: null,
    proxmox: { declared: false, guests: 0, last_error: "", backup_blind: false, node: "", cluster: null, guest_list: [], hosted: null },
    roles: [],
    role: "generic",
    modules: [],
    kind: "unknown",
    kind_override: "",
    host: null,
    ...over,
  };
}

function guest(vmid: number, node: string, over: Partial<ProxmoxMember> = {}): ProxmoxMember {
  return { vmid, kind: "qemu", name: `vm${vmid}`, status: "running", template: false, node, cpus: 1, max_memory: 1, max_disk: 1, linked_machine_id: null, ...over };
}

function hostOf(machineID: string, source: MachineHost["source"] = "guest", node = ""): MachineHost {
  return { machine_id: machineID, machine_name: machineID, node, source };
}

function hypervisor(id: string, node: string, guests: ProxmoxMember[], cluster: ProxmoxCluster | null = null, over: Partial<Machine> = {}): Machine {
  return machine(id, { proxmox: { declared: true, guests: guests.length, last_error: "", backup_blind: false, node, cluster, guest_list: guests, hosted: null }, ...over });
}

// Une ligne en une chaîne, pour lire l'arbre d'un coup d'œil : « profondeur type nom ».
function outline(rows: TreeRow[]): string[] {
  return rows.map((row) => {
    const name = row.type === "machine" ? row.machine.name : row.type === "guest" ? row.guest.name : row.type === "cluster" ? row.cluster.name : row.name;
    return `${row.depth} ${row.type} ${name}`;
  });
}

test("sans hôte, chaque machine est une racine, par nom", () => {
  expect(outline(buildMachineTree([machine("b"), machine("a")]))).toEqual(["0 machine a", "0 machine b"]);
});

test("un hôte porte ses invités enrôlés, puis ceux qu'aucun agent ne suit, par numéro", () => {
  const web = machine("web", { host: hostOf("pve", "guest", "pve") });
  const pve = hypervisor("pve", "pve", [guest(300, "pve"), guest(100, "pve", { name: "web", linked_machine_id: "web" }), guest(200, "pve")]);
  expect(outline(buildMachineTree([web, pve]))).toEqual(["0 machine pve", "1 machine web", "1 guest vm200", "1 guest vm300"]);
});

test("un modèle de VM n'est pas une machine : il n'entre pas dans l'arbre", () => {
  const pve = hypervisor("pve", "pve", [guest(9000, "pve", { template: true }), guest(100, "pve")]);
  expect(outline(buildMachineTree([pve]))).toEqual(["0 machine pve", "1 guest vm100"]);
});

test("un hôte désigné à la main porte la machine, même sans Proxmox", () => {
  const vps = machine("vps", { host: hostOf("lab", "override") });
  expect(outline(buildMachineTree([vps, machine("lab")]))).toEqual(["0 machine lab", "1 machine vps"]);
});

test("un hôte qui n'est pas dans la liste ne fait pas disparaître la machine", () => {
  expect(outline(buildMachineTree([machine("vps", { host: hostOf("ailleurs") })]))).toEqual(["0 machine vps"]);
});

test("une boucle d'hôtes montre ses machines à la racine, sans les perdre ni boucler", () => {
  const rows = buildMachineTree([machine("a", { host: hostOf("b", "override") }), machine("b", { host: hostOf("a", "override") })]);
  expect(outline(rows)).toEqual(["0 machine a", "0 machine b"]);
});

test("une machine hors ligne passe en tête avec toute sa branche, l'archivée non", () => {
  const archived = machine("a-archivee", { online: false, agent: { revoked_at: "2026-01-01T00:00:00Z" } as Machine["agent"] });
  const down = hypervisor("z-pve", "pve", [guest(1, "pve")]);
  const child = machine("web", { online: false, host: hostOf("z-pve", "guest", "pve") });
  const rows = buildMachineTree([machine("ok"), archived, down, child]);
  expect(outline(rows)).toEqual(["0 machine z-pve", "1 machine web", "1 guest vm1", "0 machine a-archivee", "0 machine ok"]);
});

const homelab: ProxmoxCluster = { id: "c1", name: "homelab" };

test("une grappe à deux nœuds ajoute son niveau ; l'invité d'un nœud sans agent est sous ce nœud", () => {
  const shared = [guest(100, "pve1"), guest(200, "pve2")];
  const pve1 = hypervisor("pve1", "pve1", shared, homelab);
  const pve2 = hypervisor("pve2", "pve2", shared, homelab);
  expect(outline(buildMachineTree([pve1, pve2]))).toEqual(["0 cluster homelab", "1 machine pve1", "2 guest vm100", "1 machine pve2", "2 guest vm200"]);
});

test("un seul hôte déclaré dans une grappe de trois : les autres nœuds sont des lignes, sans doublon", () => {
  const all = [guest(100, "pve1"), guest(200, "pve2"), guest(300, "pve3")];
  const web = machine("web", { host: hostOf("pve1", "guest", "pve3") });
  const rows = buildMachineTree([hypervisor("pve1", "pve1", all, homelab), web]);
  expect(outline(rows)).toEqual(["0 cluster homelab", "1 machine pve1", "2 guest vm100", "1 node pve2", "2 guest vm200", "1 node pve3", "2 machine web", "2 guest vm300"]);
});

test("une grappe d'un seul nœud n'ajoute aucun niveau", () => {
  const pve = hypervisor("pve", "pve", [guest(100, "pve")], homelab);
  expect(outline(buildMachineTree([pve]))).toEqual(["0 machine pve", "1 guest vm100"]);
});

test("summarizeMachines compte les machines attendues, celles qui manquent, les invités sans agent et les types inconnus", () => {
  const archived = machine("old", { agent: { revoked_at: "2026-01-01T00:00:00Z" } as Machine["agent"] });
  const machines = [machine("a", { kind: "vm" }), machine("b", { online: false }), archived, hypervisor("pve", "pve", [guest(1, "pve")])];
  const summary = summarizeMachines(machines, buildMachineTree(machines));
  expect(summary).toMatchObject({ total: 3, online: 2, guestsWithoutAgent: 1, unknownKinds: 2 });
  expect(summary.missing.map((missing) => missing.name)).toEqual(["b"]);
});
