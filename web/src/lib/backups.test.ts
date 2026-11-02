import { describe, expect, it } from "vitest";

import type { BackupPlan } from "../api/backups";
import type { Translate } from "../i18n/context";
import { backupsPath, editPlanPath, emptyPlanForm, firewallIdOf, kindPatch, isFirewallPlan, newFirewallPlanPath, lastErrorRight, lastErrorText, passSummary, passphraseProblem, planFormOf, planRequestOf, planTitle, selectableGuests, selectorIds, sourceKindsFor, sourceLabel, subjectProblem, toggleSelectorId } from "./backups";
import type { ProxmoxGuest } from "../api/proxmox";

const t: Translate = (key, ...args) => [key, ...args].join(":");

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
    expect(sourceKindsFor(false)).toEqual(["database", "agent", "firewall"]);
    expect(sourceKindsFor(true)).toEqual(["agent", "firewall"]);
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
    last_backup_storage: "",
    backup_unknown: false,
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

describe("les adresses des fenêtres de Sauvegardes", () => {
  it("s'ouvrent sur la page, le plan à corriger porte son sujet", () => {
    expect(backupsPath).toBe("/sauvegardes");
    expect(editPlanPath("pve-nuit")).toBe("/sauvegardes/plans/pve-nuit/modifier");
    expect(editPlanPath("a b")).toBe("/sauvegardes/plans/a%20b/modifier");
  });
});

it("lastErrorRight : le droit derrière une erreur de plan, et la machine dont l'agent doit l'avoir", () => {
  const destination = { machine_id: "m-dest", path: "/srv/backups" };
  const plan = (last_error: string) => ({ last_error, source_machine_id: "m-src" });
  expect(lastErrorRight(plan("backup.right_missing"), destination)).toEqual({ machineID: "m-dest", right: "backup.receive", path: "/srv/backups" });
  expect(lastErrorRight(plan("backup.source_right_missing"), destination)).toEqual({ machineID: "m-src", right: "backup.produce" });
  expect(lastErrorRight(plan("backup.target_failed:5010:right_missing:backup.produce"), destination)).toEqual({ machineID: "m-src", right: "backup.produce" });
  expect(lastErrorRight(plan("autre erreur"), destination)).toBeNull();
  expect(lastErrorRight(plan(""), undefined)).toBeNull();
  expect(lastErrorRight(plan("backup.right_missing"), undefined)).toBeNull();
});

describe("l'export d'un pare-feu", () => {
  const firewallPlan: BackupPlan = { ...agentPlan, subject: "bordure", name: "", source_machine_id: "local", source_machine_name: "opencloud", producer: "opnsense-config", selector: "fw1" };

  it("se reconnaît à son producteur et se lit dans le formulaire comme une troisième source", () => {
    expect(isFirewallPlan(firewallPlan)).toBe(true);
    expect(isFirewallPlan(agentPlan)).toBe(false);
    expect(planFormOf(firewallPlan)).toMatchObject({ kind: "firewall", selector: "fw1", subject: "bordure" });
    expect(sourceLabel(t, firewallPlan)).toBe("backup.source_firewall:opencloud");
  });

  it("part en requête « agent » sans machine : le serveur pose la machine openCloud", () => {
    const values = { ...emptyPlanForm(false, { id: "fw1", name: "Pare-feu Bordure" }), destination: "bkd_1" };
    expect(values).toMatchObject({ kind: "firewall", subject: "pare-feu-bordure", selector: "fw1" });
    expect(planRequestOf(values, "bkd_1")).toEqual({
      subject: "pare-feu-bordure",
      request: { name: "", source_kind: "agent", source_machine_id: "", producer: "opnsense-config", selector: "fw1", destination_id: "bkd_1", keep: 14 },
    });
  });

  it("propose un sujet valide même pour un nom accentué ou réservé", () => {
    expect(emptyPlanForm(true, { id: "a", name: "Épine ñ !" }).subject).toBe("epine-n");
    expect(emptyPlanForm(true, { id: "a", name: "database" }).subject).toBe("pare-feu");
    expect(emptyPlanForm(true, { id: "a", name: "***" }).subject).toBe("pare-feu");
  });

  it("garde un sujet saisi comme une source agent, la base mise à part", () => {
    expect(subjectProblem({ ...emptyPlanForm(true, { id: "a", name: "x" }), subject: "Mal" })).toBe("backup.plan_invalid");
  });

  it("réinitialise le sélecteur en changeant de source : « all » n'est pas un pare-feu", () => {
    expect(kindPatch("firewall")).toEqual({ kind: "firewall", selector: "" });
    expect(kindPatch("agent")).toEqual({ kind: "agent", selector: "all" });
    expect(kindPatch("database")).toEqual({ kind: "database", selector: "" });
  });

  it("lit l'identifiant du pare-feu dans la clé de sa source", () => {
    expect(firewallIdOf("firewall:fw1")).toBe("fw1");
    expect(newFirewallPlanPath("fw 1")).toBe("/sauvegardes/plans/nouveau?firewall=fw%201");
  });
});
