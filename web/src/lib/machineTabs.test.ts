import { expect, test } from "vitest";

import { machineTabs, renamedMachineTabs, resolveMachineTab, unreleasedMachineTabs, visibleMachineTabs } from "./machineTabs";

test("aucun onglet affiché ne mène à un écran pas encore livré", () => {
  const shown = machineTabs.map((tab) => tab.slug);
  expect(shown).toEqual(["", "services", "disponibilite", "reseau", "ssh", "journaux"]);
  for (const slug of unreleasedMachineTabs) {
    expect(shown).not.toContain(slug);
  }
});

test("resolveMachineTab renvoie un onglet pas encore livré au résumé, et l'inconnu à la page introuvable", () => {
  expect(resolveMachineTab("reseau")).toBe("shown");
  expect(resolveMachineTab("journaux")).toBe("shown");
  expect(resolveMachineTab("ssh")).toBe("shown");
  expect(resolveMachineTab("sauvegardes")).toBe("summary");
  expect(resolveMachineTab("reglages")).toBe("summary");
  expect(resolveMachineTab("nimporte")).toBe("unknown");
});

test("l'onglet Domaines et certificats est devenu Disponibilité : l'ancien segment renvoie au nouveau", () => {
  expect(resolveMachineTab("disponibilite")).toBe("shown");
  expect(resolveMachineTab("domaines")).toBe("renamed");
  expect(renamedMachineTabs["domaines"]).toBe("disponibilite");
  expect(machineTabs.map((tab) => tab.slug)).not.toContain("domaines");
});

test("l'onglet Hyperviseur n'apparaît que sur une machine qui porte un hôte Proxmox, avant les journaux", () => {
  expect(visibleMachineTabs(false).map((tab) => tab.slug)).toEqual(["", "services", "disponibilite", "reseau", "ssh", "journaux"]);
  expect(visibleMachineTabs(true).map((tab) => tab.slug)).toEqual(["", "services", "disponibilite", "reseau", "ssh", "hyperviseur", "journaux"]);
  expect(resolveMachineTab("hyperviseur")).toBe("shown");
});
