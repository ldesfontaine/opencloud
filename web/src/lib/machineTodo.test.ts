import { expect, test } from "vitest";

import type { Agent, Machine } from "../api/machines";
import { machineTodos } from "./machineTodo";

function agent(over: Partial<Agent> = {}): Agent {
  return {
    id: "a",
    version: "v0.4.2",
    enrolled_at: "2026-01-01T00:00:00Z",
    revoked_at: null,
    capabilities: [],
    reported_at: "2026-01-01T00:00:00Z",
    requested: [],
    capabilities_differ: false,
    server_key_fingerprint: "",
    run_as: null,
    rollback_capable: true,
    ...over,
  };
}

const unrelatedProxmox: Machine["proxmox"] = { declared: false, guests: 0, last_error: "", backup_blind: false, node: "", cluster: null, guest_list: [], hosted: null };

function machine(over: Partial<Machine> = {}): Machine {
  return { id: "m", name: "m", agent: agent(), kind: "vm", kind_override: "", role: "generic", roles: [], proxmox: unrelatedProxmox, ...over } as Machine;
}

test("une machine qui va n'a rien à faire", () => {
  expect(machineTodos(machine(), [], "v0.4.2")).toEqual([]);
});

test("sans agent, c'est la seule chose à dire", () => {
  expect(machineTodos(machine({ agent: null, kind: "unknown" }), [], "v0.4.2")).toEqual([{ kind: "agent_missing" }]);
});

test("une machine archivée n'a rien à faire : son agent est coupé exprès", () => {
  expect(machineTodos(machine({ agent: agent({ revoked_at: "2026-02-01T00:00:00Z" }), kind: "unknown" }), [], "v0.5.0")).toEqual([]);
});

test("un agent en retard sur le serveur propose la mise à jour, pas un agent plus récent", () => {
  expect(machineTodos(machine({ agent: agent({ version: "v0.4.1" }) }), [], "v0.4.2")).toEqual([{ kind: "agent_behind", serverVersion: "v0.4.2" }]);
  expect(machineTodos(machine({ agent: agent({ version: "v0.5.0" }) }), [], "v0.4.2")).toEqual([]);
});

test("les droits cochés que le compte ne peut pas exercer se comptent, sauf tant que l'agent n'a rien dit", () => {
  const blocked = [
    { name: "files.write", paths: [], unavailable: "path_denied" },
    { name: "observe", paths: [], unavailable: "" },
  ];
  expect(machineTodos(machine({ agent: agent({ capabilities: blocked }) }), [], "v0.4.2")).toEqual([{ kind: "rights_unavailable", count: 1 }]);
  expect(machineTodos(machine({ agent: agent({ capabilities: blocked, reported_at: null }) }), [], "v0.4.2")).toEqual([]);
});

test("la fin de support et le type inconnu se signalent, un type corrigé non", () => {
  const ended = { status: "eol" } as NonNullable<Machine["lifecycle"]>;
  expect(machineTodos(machine({ lifecycle: ended, kind: "unknown" }), [], "v0.4.2")).toEqual([{ kind: "support_ended" }, { kind: "kind_unknown", fix: { action: "correct", to: "/machines/m/emplacement" } }]);
  expect(machineTodos(machine({ kind: "unknown", kind_override: "vps" }), [], "v0.4.2")).toEqual([]);
});

test("un type inconnu dû à un agent en retard ne double pas le geste de mise à jour", () => {
  const todos = machineTodos(machine({ kind: "unknown", agent: agent({ version: "v0.2.0" }) }), [], "v0.4.2");
  expect(todos).toEqual([{ kind: "agent_behind", serverVersion: "v0.4.2" }]);
});

test("un hôte Proxmox reconnu ou annoncé par le rôle, pas déclaré, propose de le déclarer", () => {
  const seen = machine({ roles: [{ name: "proxmox_ve", version: "8.2.4" }], proxmox: unrelatedProxmox });
  expect(machineTodos(seen, [], "v0.4.2")).toEqual([{ kind: "proxmox_to_declare", version: "8.2.4" }]);
  const announced = machine({ role: "proxmox", proxmox: unrelatedProxmox });
  expect(machineTodos(announced, [], "v0.4.2")).toEqual([{ kind: "proxmox_to_declare", version: "" }]);
});

test("une VM d'un hôte déclaré n'est pas un hyperviseur, même si elle a l'air d'en être un", () => {
  const hosted = { host_machine_id: "pve" } as NonNullable<Machine["proxmox"]["hosted"]>;
  const vm = machine({ roles: [{ name: "proxmox_ve", version: "8.2.4" }], proxmox: { ...unrelatedProxmox, hosted } });
  expect(machineTodos(vm, [], "v0.4.2")).toEqual([]);
});

test("un hôte déclaré dont le relevé échoue le dit, un hôte qui lit bien se tait", () => {
  expect(machineTodos(machine({ proxmox: { ...unrelatedProxmox, declared: true, last_error: "proxmox.unauthorized" } }), [], "v0.4.2")).toEqual([{ kind: "proxmox_read_failed", key: "proxmox.unauthorized" }]);
  expect(machineTodos(machine({ proxmox: { ...unrelatedProxmox, declared: true } }), [], "v0.4.2")).toEqual([]);
});
