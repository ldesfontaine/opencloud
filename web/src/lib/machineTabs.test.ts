import { expect, test } from "vitest";

import { renamedMachineTabs, resolveMachineTab, unreleasedMachineTabs } from "./machineTabs";
import { knownMachineTabs } from "./modules";

test("aucun onglet connu ne mène à un écran pas encore livré", () => {
  const shown = knownMachineTabs.map((tab) => tab.slug);
  for (const slug of unreleasedMachineTabs) {
    expect(shown).not.toContain(slug);
  }
});

test("resolveMachineTab renvoie un onglet pas encore livré au résumé, et l'inconnu à la page introuvable", () => {
  expect(resolveMachineTab("reseau")).toBe("shown");
  expect(resolveMachineTab("journaux")).toBe("shown");
  expect(resolveMachineTab("agent")).toBe("shown");
  expect(resolveMachineTab("ssh")).toBe("shown");
  expect(resolveMachineTab("sauvegardes")).toBe("summary");
  expect(resolveMachineTab("reglages")).toBe("summary");
  expect(resolveMachineTab("nimporte")).toBe("unknown");
});

test("l'adresse d'un onglet de module reste servie, même sur une machine qui ne le porte pas", () => {
  expect(resolveMachineTab("hyperviseur")).toBe("shown");
  expect(resolveMachineTab("services")).toBe("shown");
});

test("l'onglet Domaines et certificats est devenu Disponibilité : l'ancien segment renvoie au nouveau", () => {
  expect(resolveMachineTab("disponibilite")).toBe("shown");
  expect(resolveMachineTab("domaines")).toBe("renamed");
  expect(renamedMachineTabs["domaines"]).toBe("disponibilite");
  expect(knownMachineTabs.map((tab) => tab.slug)).not.toContain("domaines");
});
