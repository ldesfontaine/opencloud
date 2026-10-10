import { expect, test } from "vitest";

import { machineTabs, resolveMachineTab, unreleasedMachineTabs } from "./machineTabs";

test("aucun onglet affiché ne mène à un écran pas encore livré", () => {
  const shown = machineTabs.map((tab) => tab.slug);
  expect(shown).toEqual(["", "services", "domaines", "reseau"]);
  for (const slug of unreleasedMachineTabs) {
    expect(shown).not.toContain(slug);
  }
});

test("resolveMachineTab renvoie un onglet pas encore livré au résumé, et l'inconnu à la page introuvable", () => {
  expect(resolveMachineTab("reseau")).toBe("shown");
  expect(resolveMachineTab("journaux")).toBe("summary");
  expect(resolveMachineTab("sauvegardes")).toBe("summary");
  expect(resolveMachineTab("reglages")).toBe("summary");
  expect(resolveMachineTab("nimporte")).toBe("unknown");
});
