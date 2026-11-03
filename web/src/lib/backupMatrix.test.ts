import { describe, expect, it } from "vitest";

import type { BackupProtection, BackupSource } from "../api/backups";
import type { Translate } from "../i18n/context";
import { matrixCells, matrixColumns, matrixGroups, nightsOf, nowhereColumn, pickedOf, placeOf } from "./backupMatrix";

const t: Translate = (key, ...args) => [key, ...args].join(":");

const protection = (overrides: Partial<BackupProtection> = {}): BackupProtection => ({
  origin: "restic",
  id: "b1",
  name: "forgejo",
  scope: "service",
  target_machine_id: "pi-id",
  target_machine_name: "pi",
  storage: "",
  archive_storage: "",
  state: "ok",
  reason: "",
  last_at: null,
  last_success_at: null,
  next_at: null,
  schedule: "daily",
  size_bytes: null,
  added_bytes: null,
  copies: 31,
  retention: { known: true, applied: true, keep_all: false, keep_last: 0, keep_daily: 14, keep_weekly: 8, keep_monthly: 3, keep_yearly: 0, last_forget: null },
  path: null,
  parts: [],
  runs: [],
  verify: null,
  ...overrides,
});

const source = (overrides: Partial<BackupSource> = {}): BackupSource => ({
  key: "service:forgejo",
  kind: "service",
  name: "forgejo",
  machine_id: "docker1-id",
  machine_name: "docker1",
  vmid: null,
  service_id: "forgejo",
  driver: "",
  used_by: [],
  observed_unknown: false,
  parts: [],
  protections: [],
  ...overrides,
});

describe("placeOf", () => {
  it("nomme la cible d'une copie restic et le stockage d'une copie Proxmox", () => {
    expect(placeOf(protection())).toEqual({ id: "machine:pi-id", name: "pi", kind: "restic" });
    expect(placeOf(protection({ origin: "observed", storage: "pi-sauvegardes" }))).toEqual({ id: "storage:pi-sauvegardes", name: "pi-sauvegardes", kind: "observed" });
  });
});

describe("matrixColumns", () => {
  it("une colonne par endroit, « Nulle part » en dernier quand une source n'a rien", () => {
    const sources = [source({ protections: [protection()] }), source({ key: "vm:1", protections: [protection({ origin: "observed", storage: "nfs" })] }), source({ key: "none" })];
    expect(matrixColumns(t, sources).map((column) => column.id)).toEqual([nowhereColumn, "storage:nfs", "machine:pi-id"]);
  });

  it("n'ajoute pas « Nulle part » quand tout est sauvegardé", () => {
    expect(matrixColumns(t, [source({ protections: [protection()] })]).map((column) => column.id)).toEqual(["machine:pi-id"]);
  });
});

describe("matrixCells", () => {
  it("dit l'état de chaque copie, le relais, l'observée, l'échec", () => {
    expect(matrixCells(t, source({ protections: [protection()] }))["machine:pi-id"]).toEqual({ tone: "ok", label: "backup.status_ok", detail: "31" });
    expect(matrixCells(t, source({ protections: [protection({ path: { path: "relay", reason: "", source_name: "", target_name: "", target_host: "", target_port: 0 } })] }))["machine:pi-id"]?.label).toBe("backup.path_relay_pill");
    expect(matrixCells(t, source({ protections: [protection({ origin: "observed", storage: "nfs" })] }))["storage:nfs"]).toMatchObject({ tone: "observed", label: "backup.matrix_observed" });
    expect(matrixCells(t, source({ protections: [protection({ state: "failed" })] }))["machine:pi-id"]).toMatchObject({ tone: "danger", label: "backup.status_protection_failed" });
    expect(matrixCells(t, source({ protections: [protection({ state: "late" })] }))["machine:pi-id"]?.tone).toBe("warn");
  });

  it("une source sans copie a la case « Nulle part », inconnue quand l'hôte n'a pas pu lire", () => {
    expect(matrixCells(t, source())).toEqual({ [nowhereColumn]: { tone: "none", label: "backup.matrix_none" } });
    expect(matrixCells(t, source({ observed_unknown: true }))[nowhereColumn]?.tone).toBe("observed");
  });
});

describe("matrixGroups", () => {
  it("groupe par machine, ce qui demande un geste en tête", () => {
    const groups = matrixGroups(t, [source({ key: "a", name: "a", protections: [protection()] }), source({ key: "b", name: "b" }), source({ key: "c", name: "c", machine_id: "vps", machine_name: "vps" })]);
    expect(groups.map((group) => group.label)).toEqual(["docker1", "vps"]);
    expect(groups[0]?.rows.map((row) => row.id)).toEqual(["b", "a"]);
  });
});

describe("pickedOf", () => {
  const sources = [source({ protections: [protection()] }), source({ key: "none", name: "none" })];
  it("retrouve la source et sa protection à cet endroit", () => {
    expect(pickedOf(sources, { row: "service:forgejo", column: "machine:pi-id" })?.protection?.id).toBe("b1");
    expect(pickedOf(sources, { row: "none", column: nowhereColumn })?.protection).toBeNull();
  });
  it("rend null quand la case n'existe plus", () => {
    expect(pickedOf(sources, null)).toBeNull();
    expect(pickedOf(sources, { row: "service:forgejo", column: "machine:ailleurs" })).toBeNull();
    expect(pickedOf(sources, { row: "service:forgejo", column: nowhereColumn })).toBeNull();
    expect(pickedOf(sources, { row: "inconnue", column: "machine:pi-id" })).toBeNull();
  });
});

describe("nightsOf", () => {
  it("de la plus ancienne à la plus récente, rouge quand un passage n'a pas réussi", () => {
    const run = (state: string) => ({ run_id: "r", finished_at: "", state, reason: "", path: "", path_reason: "", bytes_added: 0, duration_seconds: 0, parts: [] });
    expect(nightsOf(protection({ runs: [run("failed"), run("succeeded"), run("succeeded")] }))).toEqual(["none", "none", "none", "none", "ok", "ok", "danger"]);
  });
});
