import { expect, test } from "vitest";

import type { Agent, Capability, Machine } from "../api/machines";
import { heldRights, holderOfRefusal, lacksSudo, reenrollPath, rightOfRefusal, unfitFor, wantedRightOf, withWantedRight } from "./rights";

const t = (key: string, ...args: (string | number)[]) => [key, ...args].join("|");

function agentWith(requested: string[], reported: { name: string; unavailable?: string }[] | null): NonNullable<Machine["agent"]> {
  const capability = (name: string): Capability => ({ name, paths: [] });
  return {
    id: "a1",
    version: "v0.0.1",
    enrolled_at: "2026-11-01T10:00:00Z",
    revoked_at: null,
    capabilities: (reported ?? []).map((right) => ({ ...capability(right.name), unavailable: right.unavailable ?? "" })),
    reported_at: reported === null ? null : "2026-11-01T10:00:00Z",
    requested: requested.map(capability),
    capabilities_differ: false,
    server_key_fingerprint: "",
    run_as: null,
    rollback_capable: true,
  } satisfies Agent;
}

test("reenrollPath : la fenêtre de la machine, avec le droit et le chemin à cocher d'avance", () => {
  expect(reenrollPath("m1")).toBe("/machines/m1/reenroller");
  expect(reenrollPath("m1", { name: "agent.update" })).toBe("/machines/m1/reenroller?droit=agent.update");
  expect(reenrollPath("m1", { name: "backup.receive", path: "/srv/backups" })).toBe("/machines/m1/reenroller?droit=backup.receive&chemin=%2Fsrv%2Fbackups");
});

test("wantedRightOf : seul un droit de la liste fermée, hors observe, se coche", () => {
  expect(wantedRightOf(new URLSearchParams("droit=docker.deploy"))).toEqual({ name: "docker.deploy", path: "" });
  expect(wantedRightOf(new URLSearchParams("droit=backup.receive&chemin=/srv/b"))).toEqual({ name: "backup.receive", path: "/srv/b" });
  expect(wantedRightOf(new URLSearchParams("droit=observe"))).toBeNull();
  expect(wantedRightOf(new URLSearchParams("droit=rm -rf"))).toBeNull();
  expect(wantedRightOf(new URLSearchParams(""))).toBeNull();
});

test("withWantedRight : coche le droit sans doublon et ajoute son chemin à ceux qui y sont", () => {
  const values = { rights: ["probe.lan", "backup.receive"], backupPath: "/srv/a", writePaths: "", resticPath: "" };
  expect(withWantedRight(values, null)).toBe(values);
  expect(withWantedRight(values, { name: "agent.update", path: "" }).rights).toEqual(["probe.lan", "backup.receive", "agent.update"]);
  expect(withWantedRight(values, { name: "backup.receive", path: "/srv/b" })).toEqual({ rights: ["probe.lan", "backup.receive"], backupPath: "/srv/a, /srv/b", writePaths: "", resticPath: "" });
  expect(withWantedRight(values, { name: "backup.receive", path: "/srv/a" }).backupPath).toBe("/srv/a");
  expect(withWantedRight({ rights: [], backupPath: "", writePaths: "", resticPath: "" }, { name: "files.write", path: "/srv/w" })).toEqual({ rights: ["files.write"], backupPath: "", writePaths: "/srv/w", resticPath: "" });
  // Un seul dossier pour une cible restic : le voulu remplace l'ancien.
  expect(withWantedRight({ rights: [], backupPath: "", writePaths: "", resticPath: "/srv/old" }, { name: "restic.target", path: "/srv/restic" }).resticPath).toBe("/srv/restic");
});

test("rightOfRefusal : les refus qui nomment leur droit", () => {
  expect(rightOfRefusal("backup.right_missing")).toBe("backup.receive");
  expect(rightOfRefusal("backup.source_right_missing")).toBe("backup.produce");
  expect(rightOfRefusal("deploy.right_missing")).toBe("docker.deploy");
  expect(rightOfRefusal("restic.target_right_missing")).toBe("restic.target");
  expect(rightOfRefusal("restic.source_right_missing")).toBe("restic.produce");
  expect(rightOfRefusal("restic.source_vm_right_missing")).toBe("backup.produce");
  expect(rightOfRefusal("action.right_missing")).toBeNull();
});

test("heldRights : ce que l'agent a rapporté, sinon ce que son jeton demandait", () => {
  expect(heldRights(agentWith(["observe", "agent.update"], null)).map((right) => right.name)).toEqual(["observe", "agent.update"]);
  expect(heldRights(agentWith(["observe", "agent.update"], [{ name: "observe" }])).map((right) => right.name)).toEqual(["observe"]);
});

test("unfitFor : une machine convient, ou dit pourquoi pas et où aller", () => {
  const fit = { id: "m1", agent: agentWith([], [{ name: "observe" }, { name: "backup.receive" }]) };
  expect(unfitFor(t, fit, "backup.receive")).toBeNull();

  const noRight = unfitFor(t, fit, "docker.deploy");
  expect(noRight?.reason).toBe("machine.fit_right_missing|machine.capability_docker_deploy");
  expect(noRight?.link).toEqual({ to: "/machines/m1/reenroller?droit=docker.deploy", label: "machine.reenroll_with_right" });

  const noAgent = unfitFor(t, { id: "m2", agent: null }, "backup.receive");
  expect(noAgent?.reason).toBe("machine.fit_no_agent");
  expect(noAgent?.link.to).toBe("/machines/m2/reenroller");

  const revoked = unfitFor(t, { id: "m3", agent: { ...agentWith([], [{ name: "backup.receive" }]), revoked_at: "2026-11-01T10:00:00Z" } }, "backup.receive");
  expect(revoked?.reason).toBe("machine.fit_no_agent");

  const unavailable = unfitFor(t, { id: "m4", agent: agentWith([], [{ name: "docker.deploy", unavailable: "docker_denied" }]) }, "docker.deploy");
  expect(unavailable?.reason).toBe("machine.capability_unavailable_docker_denied");
  expect(unavailable?.link.to).toBe("/machines/m4");
});

test("lacksSudo : un hôte Proxmox, ou un agent qui dit que sudo manque", () => {
  const base = { role: "generic" as const, roles: [], agent: agentWith([], [{ name: "observe" }]) };
  expect(lacksSudo(base)).toBe(false);
  expect(lacksSudo({ ...base, role: "proxmox" })).toBe(true);
  expect(lacksSudo({ ...base, roles: [{ name: "proxmox_ve", version: "8.2" }] })).toBe(true);
  expect(lacksSudo({ ...base, agent: agentWith([], [{ name: "backup.produce", unavailable: "sudo_missing" }]) })).toBe(true);
  expect(lacksSudo({ ...base, agent: null })).toBe(false);
});

test("holderOfRefusal : un droit de la cible se corrige sur la cible, les autres sur la source", () => {
  const machines = { source: "docker1", target: "pi" };
  expect(holderOfRefusal("restic.target_right_missing", machines)).toBe("pi");
  expect(holderOfRefusal("restic.target_verify_right_missing", machines)).toBe("pi");
  expect(holderOfRefusal("restic.source_right_missing", machines)).toBe("docker1");
  expect(holderOfRefusal("restic.source_docker_right_missing", machines)).toBe("docker1");
  expect(holderOfRefusal("action.right_missing", machines)).toBe("docker1");
});
