import { describe, expect, test } from "vitest";

import type { SystemdUnit } from "../api/systemd";
import { activeTone, declareUnitPath, isShown, machinesWithUnits, needsPort, observedPill } from "./systemd";

function unit(name: string, active: SystemdUnit["active"], declared = false): SystemdUnit {
  return { name, load: "loaded", active, sub: "running", declared: declared ? { service_id: "svc_1", service_name: "forgejo", unit_id: "unit_1" } : null };
}

describe("isShown", () => {
  const forgejo = unit("forgejo.service", "active", true);
  const backup = unit("nightly.service", "failed");
  const cron = unit("cron.service", "inactive");

  test("le texte cherche dans le nom, sans casse ni espaces autour", () => {
    expect(isShown(forgejo, "all", " FORGE ")).toBe(true);
    expect(isShown(backup, "all", "forge")).toBe(false);
    expect(isShown(backup, "all", "")).toBe(true);
  });

  test("chaque vue garde ce qu'elle dit", () => {
    expect([forgejo, backup, cron].filter((item) => isShown(item, "running", ""))).toEqual([forgejo]);
    expect([forgejo, backup, cron].filter((item) => isShown(item, "failed", ""))).toEqual([backup]);
    expect([forgejo, backup, cron].filter((item) => isShown(item, "declared", ""))).toEqual([forgejo]);
    expect([forgejo, backup, cron].filter((item) => isShown(item, "all", ""))).toHaveLength(3);
  });

  test("la vue et le texte se cumulent", () => {
    expect(isShown(forgejo, "failed", "forge")).toBe(false);
    expect(isShown(backup, "failed", "night")).toBe(true);
  });
});

test("un état se lit par sa couleur : vert en marche, rouge en échec, orange en transition", () => {
  expect(activeTone("active")).toBe("ok");
  expect(activeTone("failed")).toBe("danger");
  expect(activeTone("activating")).toBe("warn");
  expect(activeTone("inactive")).toBe("neutral");
  expect(activeTone("inconnu")).toBe("neutral");
});

test("seul un binaire sans port invite à le déclarer", () => {
  expect(needsPort("binary", [])).toBe(true);
  expect(needsPort("binary", ["3000"])).toBe(false);
  expect(needsPort("container", [])).toBe(false);
});

test("la pastille d'une unité observée dit son état, son absence ou qu'elle n'est pas relevée", () => {
  const found = { presence: "found" as const, load: "loaded", active: "failed", sub: "failed", reported_at: null };
  expect(observedPill(found)).toEqual({ key: "systemd.active_failed", tone: "danger" });
  expect(observedPill({ ...found, presence: "missing" })).toEqual({ key: "systemd.presence_missing", tone: "danger" });
  expect(observedPill({ ...found, presence: "unknown" })).toEqual({ key: "systemd.presence_unknown", tone: "neutral" });
});

test("déclarer une unité ouvre l'assistant sur la machine et l'unité, noms échappés", () => {
  expect(declareUnitPath("m1", "getty@tty1.service")).toBe("/services/annuaire/nouveau?machine=m1&unit=getty%40tty1.service");
});

test("seules les machines qui ont relevé leurs unités sont proposées", () => {
  const machines = [
    { id: "a", modules: ["containers"] },
    { id: "b", modules: ["containers", "systemd_units"] },
  ];
  expect(machinesWithUnits(machines).map((machine) => machine.id)).toEqual(["b"]);
});
