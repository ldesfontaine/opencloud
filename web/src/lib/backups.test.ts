import { describe, expect, it } from "vitest";

import type { BackupPlan, Backups } from "../api/backups";
import type { Translate } from "../i18n/context";
import { emptyPlanForm, isBackedUp, passphraseProblem, planFormOf, planRequestOf, planTitle, sourceLabel, subjectProblem } from "./backups";

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
});
