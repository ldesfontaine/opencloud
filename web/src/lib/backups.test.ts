import { describe, expect, it } from "vitest";

import type { BackupPlan, BackupRun, BackupSource, Backups } from "../api/backups";
import type { Translate } from "../i18n/context";
import { emptyPlanForm, failedAttemptOf, isBackedUp, lastBackupOf, protectionOf, runReasonText, lastErrorText, passSummary, passphraseProblem, planFormOf, planRequestOf, planTitle, selectableGuests, selectorIds, sourceKindsFor, sourceLabel, subjectProblem, toggleSelectorId } from "./backups";
import type { ProxmoxGuest } from "../api/proxmox";

const t: Translate = (key, ...args) => [key, ...args].join(":");

const keyring = { recipient: "age1x", fingerprint: "0123456789abcdef", created_at: "2026-09-12T12:00:00Z" };
const destination = { id: "bkd_1", name: "store-1", machine_id: "m1", machine_name: "store-1", path: "/srv/backups", right: "ok" as const, created_at: "2026-09-12T12:00:00Z" };
const databasePlan: BackupPlan = {
  subject: "database",
  name: "",
  source_kind: "database",
  source_machine_id: "",
  source_machine_name: "",
  producer: "",
  selector: "",
  destination_id: "bkd_1",
  keep: 14,
  schedule: "daily",
  last_attempt_at: null,
  last_error: "",
  last_run: null,
  last_targets_ok: 0,
  last_targets_failed: 0,
};
const agentPlan: BackupPlan = {
  ...databasePlan,
  subject: "pve-vm",
  name: "Machines virtuelles",
  source_kind: "agent",
  source_machine_id: "m2",
  source_machine_name: "pve",
  producer: "proxmox-vzdump",
  selector: "5010,5012",
  keep: 7,
};

describe("passphraseProblem", () => {
  it("refuses two phrases that differ, before saying anything of their length", () => {
    expect(passphraseProblem("une phrase de test assez longue", "une phrase de test assez longuE")).toBe("backup.passphrase_mismatch");
    expect(passphraseProblem("court", "autre")).toBe("backup.passphrase_mismatch");
  });

  it("refuses a short phrase, counted in characters rather than bytes", () => {
    expect(passphraseProblem("court", "court")).toBe("backup.passphrase_too_short");
    // 15 caractères, 19 octets : trop court.
    expect(passphraseProblem("é".repeat(15), "é".repeat(15))).toBe("backup.passphrase_too_short");
    expect(passphraseProblem("é".repeat(16), "é".repeat(16))).toBeNull();
  });

  it("does not count the spaces at the ends, as the server does", () => {
    expect(passphraseProblem("  quinze caractèr  ", "  quinze caractèr  ")).toBe("backup.passphrase_too_short");
  });
});

describe("isBackedUp", () => {
  it("needs a key and the database plan with its destination", () => {
    expect(isBackedUp({ keyring, destinations: [destination], plans: [databasePlan] })).toBe(true);
    expect(isBackedUp({ keyring: null, destinations: [destination], plans: [databasePlan] })).toBe(false);
    expect(isBackedUp({ keyring, destinations: [destination], plans: [] })).toBe(false);
    expect(isBackedUp({ keyring, destinations: [], plans: [databasePlan] })).toBe(false);
  });

  it("is not backed up by a plan that points elsewhere", () => {
    const elsewhere: Backups = { keyring, destinations: [destination], plans: [{ ...databasePlan, destination_id: "bkd_2" }] };
    expect(isBackedUp(elsewhere)).toBe(false);
  });

  it("is not backed up by the plan of another source", () => {
    expect(isBackedUp({ keyring, destinations: [destination], plans: [agentPlan] })).toBe(false);
  });
});

describe("planTitle", () => {
  it("shows the label, else the subject, and names the database by its own label", () => {
    expect(planTitle(agentPlan, "La base")).toBe("Machines virtuelles");
    expect(planTitle({ ...agentPlan, name: "" }, "La base")).toBe("pve-vm");
    expect(planTitle(databasePlan, "La base")).toBe("La base");
    expect(planTitle({ ...databasePlan, name: "Nuit" }, "La base")).toBe("Nuit");
  });
});

describe("sourceLabel", () => {
  it("names the database by its own label", () => {
    expect(sourceLabel(t, databasePlan)).toBe("backup.source_kind_database");
  });

  it("names the machine, the producer and what it takes", () => {
    expect(sourceLabel(t, { ...agentPlan, selector: "all" })).toBe("backup.source_agent:pve:backup.producer_proxmox-vzdump:backup.selector_all");
    expect(sourceLabel(t, agentPlan)).toBe("backup.source_agent:pve:backup.producer_proxmox-vzdump:backup.selector_some:5010,5012");
  });
});

describe("the form of a plan", () => {
  it("starts with the database while it has no plan, then with an agent source", () => {
    expect(emptyPlanForm(false).kind).toBe("database");
    expect(emptyPlanForm(true).kind).toBe("agent");
    expect(emptyPlanForm(true).selector).toBe("all");
  });

  it("is filled from a plan, and gives it back unchanged", () => {
    const values = planFormOf(agentPlan);
    expect(values).toMatchObject({ subject: "pve-vm", kind: "agent", machine: "m2", selector: "5010,5012", destination: "bkd_1", keep: "7" });
    expect(planRequestOf(values, values.destination)).toEqual({
      subject: "pve-vm",
      request: {
        name: "Machines virtuelles",
        source_kind: "agent",
        source_machine_id: "m2",
        producer: "proxmox-vzdump",
        selector: "5010,5012",
        destination_id: "bkd_1",
        keep: 7,
      },
    });
  });

  it("writes the database with its fixed subject and no machine, producer or selector", () => {
    const values = { ...planFormOf(agentPlan), kind: "database" as const, subject: "peu importe" };
    expect(planRequestOf(values, "bkd_2")).toEqual({
      subject: "database",
      request: { name: "Machines virtuelles", source_kind: "database", source_machine_id: "", producer: "", selector: "", destination_id: "bkd_2", keep: 7 },
    });
  });

  it("trims the label and the selector", () => {
    const values = { ...planFormOf(agentPlan), name: "  Nuit  ", selector: " all " };
    const { request } = planRequestOf(values, "bkd_1");
    expect(request.name).toBe("Nuit");
    expect(request.selector).toBe("all");
  });
});

describe("subjectProblem", () => {
  const agent = planFormOf(agentPlan);
  it("accepts a word of lowercase letters, digits and dashes", () => {
    expect(subjectProblem(agent)).toBeNull();
    expect(subjectProblem({ ...agent, subject: "a".repeat(40) })).toBeNull();
  });

  it("refuses what the server refuses, in the same key", () => {
    for (const subject of ["", "Pve", "pve vm", "pve/vm", "-pve", "é", "a".repeat(41), "database"]) {
      expect(subjectProblem({ ...agent, subject })).toBe("backup.plan_invalid");
    }
  });

  it("does not ask the database for a subject", () => {
    expect(subjectProblem({ ...agent, kind: "database", subject: "" })).toBeNull();
  });

  // PUT crée ou remplace : un plan neuf qui reprendrait un sujet écraserait
  // le plan qui le porte.
  it("refuses a new plan that takes the subject of an existing one, the database included", () => {
    expect(subjectProblem(agent, ["database", "pve-vm"])).toBe("backup.plan_subject_taken");
    expect(subjectProblem({ ...agent, kind: "database", subject: "" }, ["database"])).toBe("backup.plan_subject_taken");
    expect(subjectProblem({ ...agent, subject: "pve-ct" }, ["database", "pve-vm"])).toBeNull();
  });
});

describe("sourceKindsFor", () => {
  it("offers the database to a new plan only while it has none", () => {
    expect(sourceKindsFor(false)).toEqual(["database", "agent"]);
    expect(sourceKindsFor(true)).toEqual(["agent"]);
  });
});

describe("lastErrorText", () => {
  const describeReason = (reason: string) => `reason(${reason})`;

  it("translates a refusal key as it is", () => {
    expect(lastErrorText(t, "backup.source_offline", describeReason)).toBe("backup.source_offline");
  });

  it("names the virtual machine and the reason of a target that failed", () => {
    expect(lastErrorText(t, "backup.target_failed:5010:producer_failed", describeReason)).toBe("backup.target_failed:5010:reason(producer_failed)");
    expect(lastErrorText(t, "backup.prune_failed:5010:path_unwritable", describeReason)).toBe("backup.prune_failed:5010:reason(path_unwritable)");
  });

  it("keeps the right that a reason names after its own colon", () => {
    expect(lastErrorText(t, "backup.target_failed:7:right_missing:backup.produce", describeReason)).toBe("backup.target_failed:7:reason(right_missing:backup.produce)");
  });

  it("says a reason was not reported rather than guessing one", () => {
    expect(lastErrorText(t, "backup.target_failed:7:unknown", describeReason)).toBe("backup.target_failed:7:backup.target_reason_unknown");
    expect(lastErrorText(t, "backup.target_failed:7", describeReason)).toBe("backup.target_failed:7:backup.target_reason_unknown");
  });
});

describe("passSummary", () => {
  it("says nothing of a plan that has not passed since the server started", () => {
    expect(passSummary(t, agentPlan)).toBeNull();
  });

  it("gives the two counters, the failed ones even when they are none", () => {
    expect(passSummary(t, { last_targets_ok: 11, last_targets_failed: 0 })).toBe("backup.targets_ok:11 · backup.targets_failed:0");
    expect(passSummary(t, { last_targets_ok: 9, last_targets_failed: 2 })).toBe("backup.targets_ok:9 · backup.targets_failed:2");
  });
});

describe("the choice of virtual machines", () => {
  const guest = (vmid: number, template = false): ProxmoxGuest => ({
    vmid,
    kind: "qemu",
    name: `vm-${vmid}`,
    status: "running",
    template,
    node: "pve",
    cpus: 1,
    max_memory: 0,
    max_disk: 0,
    linked_machine_id: null,
    last_backup_at: null,
    last_backup_bytes: 0,
    own_snapshots: [],
  });

  it("offers the guests by number, templates left out", () => {
    expect(selectableGuests([guest(300), guest(100, true), guest(200)]).map((item) => item.vmid)).toEqual([200, 300]);
  });

  it("reads the numbers of a selector, and none for all", () => {
    expect(selectorIds("5010,5012")).toEqual([5010, 5012]);
    expect(selectorIds("all")).toEqual([]);
    expect(selectorIds("")).toEqual([]);
  });

  it("keeps the selector sorted and without duplicates when a box is toggled", () => {
    expect(toggleSelectorId("", 200, true)).toBe("200");
    expect(toggleSelectorId("200", 100, true)).toBe("100,200");
    expect(toggleSelectorId("100,200", 200, true)).toBe("100,200");
    expect(toggleSelectorId("100,200", 100, false)).toBe("200");
    expect(toggleSelectorId("200", 200, false)).toBe("");
  });
});

const run = (overrides: Partial<BackupRun>): BackupRun => ({
  plan: "pve",
  source: "guest:m1:100",
  pass_at: "2026-10-30T03:00:00Z",
  started_at: "2026-10-30T03:00:00Z",
  finished_at: "2026-10-30T03:10:00Z",
  result: "ok",
  reason: "",
  size_bytes: 1000,
  destination_id: "bkd_1",
  destination_name: "store-1",
  action_run_id: "run_1",
  ...overrides,
});

const source = (overrides: Partial<BackupSource>): BackupSource => ({
  key: "guest:m1:100",
  kind: "vm",
  name: "web",
  machine_id: "m1",
  machine_name: "pve",
  id: "100",
  driver: "",
  used_by: [],
  covered_by: [],
  observed: null,
  last_run: null,
  last_ok: null,
  ...overrides,
});

const observed = { storage: "nfs", count: 3, total_bytes: 900, last_at: "2026-10-31T02:00:00Z", last_bytes: 300 };
const coverage = { subject: "pve", plan_name: "", destination_id: "bkd_1", destination_name: "store-1" };

describe("inventory protection", () => {
  it("tells apart a source nothing saves, one only Proxmox keeps, and one a plan covers", () => {
    expect(protectionOf(source({}))).toBe("none");
    expect(protectionOf(source({ observed }))).toBe("observed");
    expect(protectionOf(source({ observed, covered_by: [coverage] }))).toBe("covered");
  });

  it("shows the most recent of the last plan success and the newest archive Proxmox keeps", () => {
    const older = run({ finished_at: "2026-10-30T03:10:00Z" });
    expect(lastBackupOf(source({}))).toBeNull();
    expect(lastBackupOf(source({ last_ok: older }))).toEqual({ at: "2026-10-30T03:10:00Z", bytes: 1000, target: "store-1", origin: "plan" });
    expect(lastBackupOf(source({ observed }))).toEqual({ at: "2026-10-31T02:00:00Z", bytes: 300, target: "nfs", origin: "observed" });
    expect(lastBackupOf(source({ observed, last_ok: older }))?.origin).toBe("observed");
    expect(lastBackupOf(source({ observed, last_ok: run({ finished_at: "2026-11-01T03:10:00Z" }) }))?.origin).toBe("plan");
  });

  it("reports an attempt only when it failed after the last success", () => {
    const success = run({ finished_at: "2026-10-30T03:10:00Z" });
    const failure = run({ result: "failed", reason: "timeout", finished_at: "2026-10-31T03:10:00Z" });
    expect(failedAttemptOf(source({ last_run: success, last_ok: success }))).toBeNull();
    expect(failedAttemptOf(source({ last_run: failure, last_ok: success }))).toBe(failure);
    expect(failedAttemptOf(source({ last_run: failure }))).toBe(failure);
    expect(failedAttemptOf(source({ last_run: run({ result: "refused", finished_at: "2026-10-29T03:10:00Z" }), last_ok: success }))).toBeNull();
  });

  it("translates a refusal key as is and sends a closed word to the order decoder", () => {
    expect(runReasonText(t, "", () => "decoded")).toBe("");
    expect(runReasonText(t, "backup.machine_offline", () => "decoded")).toBe("backup.machine_offline");
    expect(runReasonText(t, "disk_full", (reason) => `decoded ${reason}`)).toBe("decoded disk_full");
  });
});
