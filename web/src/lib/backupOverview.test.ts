import { describe, expect, it } from "vitest";

import type { BackupMachine, BackupOverview, BackupPath, BackupProtection, BackupRetention, BackupSource, BackupTarget } from "../api/backups";
import type { Translate } from "../i18n/context";
import {
  candidatesFor,
  declaredRetention,
  directHint,
  diskShare,
  expectedPath,
  filterSources,
  floorText,
  forgetView,
  guestArchiveName,
  hostPort,
  isRestServerMissing,
  isResticMissing,
  matchesSearch,
  nextRunAt,
  pathReasonText,
  protectionReasonText,
  repositoryUser,
  restoreVMID,
  retentionView,
  sortSources,
  sourceStatus,
  sourceUnfit,
  targetChoices,
  targetFloorText,
  targetSetupUnfit,
  verdictOf,
} from "./backupOverview";

const t: Translate = (key, ...args) => [key, ...args].join(":");

const retention = (overrides: Partial<BackupRetention> = {}): BackupRetention => ({
  known: true,
  applied: true,
  keep_all: false,
  keep_last: 0,
  keep_daily: 14,
  keep_weekly: 8,
  keep_monthly: 3,
  keep_yearly: 0,
  last_forget: null,
  ...overrides,
});

const protection = (overrides: Partial<BackupProtection> = {}): BackupProtection => ({
  origin: "restic",
  id: "b1",
  name: "paperless",
  scope: "service",
  target_machine_id: "pi",
  target_machine_name: "pi",
  storage: "",
  archive_storage: "",
  state: "ok",
  reason: "",
  last_at: "2026-09-12T02:00:00Z",
  next_at: "2026-09-13T02:00:00Z",
  schedule: "daily",
  size_bytes: null,
  added_bytes: 1024,
  copies: null,
  retention: retention(),
  path: null,
  runs: [],
  ...overrides,
});

const source = (overrides: Partial<BackupSource> = {}): BackupSource => ({
  key: "service:svc_1",
  kind: "service",
  name: "paperless",
  machine_id: "docker1",
  machine_name: "docker1",
  vmid: null,
  service_id: "svc_1",
  driver: "",
  used_by: [],
  observed_unknown: false,
  protections: [],
  ...overrides,
});

const right = (name: string, state: BackupMachine["rights"][number]["state"], path = "") => ({ right: name, state, reason: "", path });

const machine = (id: string, rights: BackupMachine["rights"], overrides: Partial<BackupMachine> = {}): BackupMachine => ({
  machine_id: id,
  machine_name: id,
  os: "Debian 12",
  arch: "amd64",
  online: true,
  restic_available: true,
  is_target: false,
  proxmox_host: false,
  proxmox_jobs_unknown: false,
  rights,
  ...overrides,
});

const target = (overrides: Partial<BackupTarget> = {}): BackupTarget => ({
  key: "restic:pi",
  kind: "restic",
  name: "pi",
  machine_id: "pi",
  machine_name: "pi",
  online: true,
  state: "running",
  reason: "",
  host: "192.0.2.10",
  port: 8000,
  dir: "/srv/restic",
  version: "rest-server 0.14.0",
  fingerprint: "ab",
  disk_total_bytes: 2000,
  disk_free_bytes: 600,
  disk_used_bytes: 1400,
  backup_bytes: 1000,
  reported_at: null,
  retention_floor: { keep_daily: 7, keep_weekly: 0, keep_monthly: 0 },
  node: "",
  type: "",
  holds: [],
  ...overrides,
});

const relay: BackupPath = { path: "relay", reason: "tcp_unreachable", source_name: "vps", target_name: "pi", target_host: "192.0.2.10", target_port: 8000 };

describe("l'état d'une source", () => {
  it("dit l'échec avant tout, sinon une copie réussie suffit, et rien n'est pas inconnu", () => {
    expect(sourceStatus(source())).toBe("none");
    expect(sourceStatus(source({ observed_unknown: true }))).toBe("unknown");
    expect(sourceStatus(source({ protections: [protection({ state: "never" })] }))).toBe("never");
    expect(sourceStatus(source({ protections: [protection({ state: "never" }), protection({ origin: "observed", state: "ok" })] }))).toBe("ok");
    expect(sourceStatus(source({ protections: [protection({ state: "ok" }), protection({ state: "refused" })] }))).toBe("failed");
    expect(sourceStatus(source({ protections: [protection({ state: "late" }), protection({ state: "unknown" })] }))).toBe("late");
  });

  it("range ce qui demande un geste en tête, puis par machine et par nom", () => {
    const ok = source({ key: "a", name: "a", protections: [protection()] });
    const none = source({ key: "b", name: "b", machine_name: "z" });
    const failed = source({ key: "c", name: "c", protections: [protection({ state: "failed" })] });
    const otherNone = source({ key: "d", name: "d", machine_name: "a" });
    expect(sortSources([ok, none, failed, otherNone]).map((item) => item.key)).toEqual(["c", "d", "b", "a"]);
    expect(filterSources([ok, none, failed], "attention").map((item) => item.key)).toEqual(["b", "c"]);
    expect(filterSources([ok, none, failed], "protected").map((item) => item.key)).toEqual(["a"]);
    expect(filterSources([ok, none, failed], "all")).toHaveLength(3);
  });

  it("compose le verdict : le danger d'un échec, la première source à traiter", () => {
    const failed = source({ key: "c", protections: [protection({ state: "failed" })] });
    expect(verdictOf([source({ protections: [protection()] })])).toMatchObject({ tone: "ok", protectedCount: 1, attention: 0, first: null });
    expect(verdictOf([source({ protections: [protection()] }), source({ key: "n" })])).toMatchObject({ tone: "warn", attention: 1 });
    expect(verdictOf([source({ key: "n" }), failed])).toMatchObject({ tone: "danger", failed: 1, first: failed });
    expect(verdictOf([source({ observed_unknown: true })]).tone).toBe("neutral");
  });

  it("trouve la prochaine exécution à venir, pas une échéance passée", () => {
    const now = Date.parse("2026-09-12T12:00:00Z");
    const sources = [source({ protections: [protection({ next_at: "2026-09-12T11:00:00Z" }), protection({ next_at: "2026-09-13T03:00:00Z" }), protection({ next_at: "2026-09-13T02:00:00Z" })] })];
    expect(nextRunAt(sources, now)).toBe("2026-09-13T02:00:00Z");
    expect(nextRunAt([source()], now)).toBeNull();
  });
});

describe("la rétention en clair", () => {
  it("dit les nombres gardés, au singulier quand il n'y en a qu'un", () => {
    expect(declaredRetention(t, retention())).toBe("backup.retention_daily:14 · backup.retention_weekly:8 · backup.retention_monthly:3");
    expect(declaredRetention(t, retention({ keep_daily: 1, keep_weekly: 0, keep_monthly: 0, keep_last: 3 }))).toBe("backup.retention_last:3 · backup.retention_daily_one:1");
    expect(declaredRetention(t, retention({ keep_all: true }))).toBeNull();
  });

  it("dit ce qui est gardé et qui l'applique : la cible chaque nuit, l'hôte à chaque tâche", () => {
    expect(retentionView(t, protection(), "docker1")).toEqual({
      value: "backup.retention_daily:14 · backup.retention_weekly:8 · backup.retention_monthly:3",
      note: "backup.retention_by_target:pi",
    });
    expect(retentionView(t, protection({ origin: "observed" }), "pve").note).toBe("backup.retention_by_proxmox:pve");
    expect(retentionView(t, protection({ origin: "archive", retention: retention({ keep_last: 7, keep_daily: 0, keep_weekly: 0, keep_monthly: 0 }) }), "local")).toEqual({
      value: "backup.retention_last:7",
      note: "backup.retention_by_server",
    });
    expect(retentionView(t, protection({ origin: "observed", retention: retention({ keep_all: true }) }), "pve").value).toBe("backup.retention_all");
    expect(retentionView(t, protection({ retention: retention({ known: false }) }), "docker1")).toEqual({ value: "backup.retention_unknown", note: null });
  });

  it("dit la dernière purge : pas encore, en cours, faite, ou ce qui l'a empêchée", () => {
    const now = Date.parse("2026-09-12T12:00:00Z");
    const decode = (reason: string) => `decoded ${reason}`;
    expect(forgetView(t, null, now, decode)).toEqual({ text: "backup.forget_none", tone: "neutral" });
    expect(forgetView(t, { at: "2026-09-12T03:00:00Z", state: "succeeded", reason: "" }, now, decode).text).toMatch(/^backup\.forget_succeeded:job\.ago:/);
    expect(forgetView(t, { at: "2026-09-12T03:00:00Z", state: "sent", reason: "" }, now, decode).tone).toBe("neutral");
    const refused = forgetView(t, { at: "2026-09-12T03:00:00Z", state: "refused", reason: "retention_below_floor" }, now, decode);
    expect(refused.tone).toBe("warn");
    expect(refused.text).toMatch(/^backup\.forget_refused:.*:decoded retention_below_floor$/);
    expect(forgetView(t, { at: null, state: "failed", reason: "restic.link_secret_lost" }, now, decode).text).toBe("backup.forget_failed::restic.link_secret_lost");
  });

  it("dit le plancher d'une cible, ou qu'elle n'en a pas encore rapporté", () => {
    expect(floorText(t, { keep_daily: 7, keep_weekly: 0, keep_monthly: 0 })).toBe("backup.retention_daily:7");
    expect(floorText(t, { keep_daily: 0, keep_weekly: 0, keep_monthly: 0 })).toBeNull();
    expect(targetFloorText(t, target())).toBe("backup.target_floor:pi:backup.retention_daily:7:pi");
    expect(targetFloorText(t, target({ retention_floor: null }))).toBe("backup.target_floor_unknown:pi");
    expect(targetFloorText(t, target({ retention_floor: { keep_daily: 0, keep_weekly: 0, keep_monthly: 0 } }))).toBe("backup.target_floor_none:pi");
  });
});

describe("le chemin direct ou relais", () => {
  it("compose ce qu'il faut ouvrir à partir des champs bruts", () => {
    expect(directHint(t, relay)).toBe("backup.path_direct_hint:vps:192.0.2.10:8000");
    expect(hostPort("2001:db8::1", 8000)).toBe("[2001:db8::1]:8000");
  });

  it("dit pourquoi : la cible répond, ou ce qui a manqué", () => {
    expect(pathReasonText(t, relay)).toBe("backup.path_reason_tcp_unreachable:vps:192.0.2.10:8000");
    expect(pathReasonText(t, { ...relay, path: "direct", reason: "reachable" })).toBe("backup.path_reason_reachable:pi");
    expect(pathReasonText(t, { ...relay, reason: "tls_failed" })).toBe("backup.path_reason_tls_failed:vps:pi");
  });

  it("reprend le chemin connu d'une même source vers une même cible", () => {
    const sources = [source({ protections: [protection({ path: relay })] })];
    expect(expectedPath(sources, "docker1", "pi")).toBe(relay);
    expect(expectedPath(sources, "docker1", "nas")).toBeNull();
    expect(expectedPath(sources, "vps", "pi")).toBeNull();
  });

  it("traduit une clé de refus telle quelle, un mot fermé par le décodeur des ordres", () => {
    expect(protectionReasonText(t, "restic.source_right_missing", () => "decoded")).toBe("restic.source_right_missing");
    expect(protectionReasonText(t, "timeout", (reason) => `decoded ${reason}`)).toBe("decoded timeout");
    expect(protectionReasonText(t, "", () => "decoded")).toBe("");
  });
});

describe("le disque d'une cible", () => {
  it("se partage entre les sauvegardes, le reste de l'occupé et le libre", () => {
    expect(diskShare(target())).toEqual({ backup: 1000, other: 400, free: 600, backupPercent: 50, otherPercent: 20 });
    // Un dépôt plus gros que l'occupé rapporté ne déborde pas la jauge.
    expect(diskShare(target({ backup_bytes: 5000 }))?.other).toBe(0);
    expect(diskShare(target({ disk_total_bytes: 0 }))).toBeNull();
  });
});

describe("l'assistant", () => {
  const machines = [
    machine("docker1", [right("restic.produce", "ok"), right("backup.produce", "missing")]),
    machine("pve", [right("restic.produce", "ok"), right("backup.produce", "ok")], { online: false }),
    machine("pi", [right("restic.target", "ok", "/srv/restic")], { is_target: true }),
    machine("nas", [right("restic.target", "ok", "/srv/restic")]),
    machine("vps", [right("restic.target", "missing"), right("restic.produce", "missing")]),
  ];

  it("propose les services ou les VM, et cherche sans casse ni accent", () => {
    const vm = source({ key: "guest:pve:101", kind: "vm", name: "Été", machine_id: "pve", machine_name: "pve", vmid: 101 });
    expect(candidatesFor([source(), vm], "vm")).toEqual([vm]);
    expect(candidatesFor([source(), vm], "service").map((item) => item.key)).toEqual(["service:svc_1"]);
    expect(matchesSearch(vm, "ete")).toBe(true);
    expect(matchesSearch(vm, "PVE")).toBe(true);
    expect(matchesSearch(vm, "docker")).toBe(false);
  });

  it("grise une source dont la machine manque d'un droit, avec le droit à donner", () => {
    expect(sourceUnfit(t, source(), machines)).toBeNull();
    expect(sourceUnfit(t, source({ machine_id: "vps", machine_name: "vps" }), machines)?.right).toEqual({ machineID: "vps", name: "restic.produce" });
    const vmOnDocker = source({ kind: "vm", machine_id: "docker1", vmid: 1 });
    expect(sourceUnfit(t, vmOnDocker, machines)?.right).toEqual({ machineID: "docker1", name: "backup.produce" });
    expect(sourceUnfit(t, source({ kind: "vm", machine_id: "pve", machine_name: "pve", vmid: 1 }), machines)?.reason).toBe("backup.unfit_offline:pve");
  });

  it("grise une source sans restic, avec la machine où l'installer", () => {
    const atelier = machine("atelier", [right("restic.produce", "unavailable")], { restic_available: false });
    const lacking = machine("atelier", [{ right: "restic.produce", state: "unavailable", reason: "restic_missing", path: "" }], { restic_available: false });
    const onAtelier = source({ machine_id: "atelier", machine_name: "atelier" });
    expect(isResticMissing(lacking)).toBe(true);
    expect(isResticMissing(atelier)).toBe(false);
    expect(isResticMissing(machine("vps", [right("restic.produce", "missing")], { restic_available: false }))).toBe(false);
    expect(sourceUnfit(t, onAtelier, [lacking])).toEqual({ reason: "backup.unfit_restic_missing:atelier", install: { software: "restic", machine: lacking } });
    expect(sourceUnfit(t, onAtelier, [atelier])?.install).toBeUndefined();
  });

  it("grise une cible sans rest-server, avec la machine où l'installer", () => {
    const missing = target({ key: "restic:coffre", name: "coffre", machine_id: "coffre", machine_name: "coffre", state: "error", reason: "binary_missing" });
    expect(isRestServerMissing(missing)).toBe(true);
    expect(isRestServerMissing(target({ state: "error", reason: "port_in_use" }))).toBe(false);
    const coffre = machine("coffre", [right("restic.target", "ok", "/srv/restic")], { is_target: true, arch: "arm64" });
    const overview: BackupOverview = { sources: [], targets: [target(), missing], machines: [...machines, coffre] };
    const choice = targetChoices(t, overview, "docker1", (id) => id).find((candidate) => candidate.machine.machine_id === "coffre");
    expect(choice?.unfit).toEqual({ reason: "backup.unfit_rest_server_missing", install: { software: "rest-server", machine: coffre } });
    // Pas encore cible : son agent a déjà dit que rest-server manque.
    const bare = machine("grenier", [{ right: "restic.target", state: "unavailable", reason: "rest_server_missing", path: "/srv/restic" }]);
    const fresh = targetChoices(t, { ...overview, machines: [bare] }, "docker1", (id) => id);
    expect(fresh[0]?.unfit?.install?.software).toBe("rest-server");
    expect(targetSetupUnfit(t, bare, "grenier")?.reason).toBe("backup.unfit_rest_server_missing");
  });

  it("met les cibles prêtes en tête, et dit pour les autres quoi faire", () => {
    const overview: BackupOverview = { sources: [], targets: [target()], machines };
    const choices = targetChoices(t, overview, "docker1", (id) => `/cible/${id}`);
    expect(choices.map((choice) => choice.machine.machine_id)).toEqual(["pi", "docker1", "nas", "pve", "vps"]);
    expect(choices[0]?.unfit).toBeNull();
    expect(choices.find((choice) => choice.machine.machine_id === "docker1")?.unfit?.reason).toBe("backup.unfit_same_machine");
    expect(choices.find((choice) => choice.machine.machine_id === "nas")?.unfit?.link?.to).toBe("/cible/nas");
    expect(choices.find((choice) => choice.machine.machine_id === "vps")?.unfit?.right).toEqual({ machineID: "vps", name: "restic.target" });
    const pending = targetChoices(t, { ...overview, targets: [target({ state: "pending" })] }, "docker1", (id) => id);
    expect(pending[0]?.unfit?.reason).toBe("backup.unfit_target_pending");
  });

  it("ne propose comme nouvelle cible qu'une machine qui a le droit et ne l'est pas déjà", () => {
    expect(targetSetupUnfit(t, machines[3], "nas")).toBeNull();
    expect(targetSetupUnfit(t, machines[2], "pi")?.reason).toBe("backup.unfit_already_target");
    expect(targetSetupUnfit(t, machines[4], "vps")?.link.to).toBe("/machines/vps/reenroller?droit=restic.target");
  });
});

describe("accéder à une sauvegarde", () => {
  it("nomme l'archive d'un invité comme l'agent la garde, et un numéro voisin pour la restaurer", () => {
    expect(guestArchiveName({ kind: "vm", vmid: 105 })).toBe("/vzdump-qemu-105.vma");
    expect(guestArchiveName({ kind: "lxc", vmid: 104 })).toBe("/vzdump-lxc-104.tar");
    expect(restoreVMID(105)).toBe(1105);
    expect(repositoryUser("AB-12")).toBe("ab-12");
  });
});
