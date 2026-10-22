import { expect, test } from "vitest";

import { currentSection, settingsSections } from "./settingsSections";

const order = ["acces", "alertes", "e-mails", "fin-de-support", "agent-ia"];

test("les ancres sont sans accent ni espace, et propres à chaque section", () => {
  const ids = settingsSections.map((section) => section.id);
  expect(new Set(ids).size).toBe(ids.length);
  for (const id of ids) {
    expect(id).toMatch(/^[a-z0-9-]+$/);
  }
});

test("la section en évidence est la dernière de la bande de lecture", () => {
  expect(currentSection(order, new Set(["alertes"]), null, false)).toBe("alertes");
  expect(currentSection(order, new Set(["alertes", "e-mails"]), "alertes", false)).toBe("e-mails");
  expect(currentSection(order, new Set(["e-mails", "alertes"]), null, false)).toBe("e-mails");
});

test("entre deux sections, la précédente reste en évidence", () => {
  expect(currentSection(order, new Set(), "e-mails", false)).toBe("e-mails");
});

test("sans précédente connue, la première ; sans section, rien", () => {
  expect(currentSection(order, new Set(), null, false)).toBe("acces");
  expect(currentSection(order, new Set(), "disparue", false)).toBe("acces");
  expect(currentSection([], new Set(), null, false)).toBeNull();
});

test("en bas de page, la dernière section, même si une autre traverse la bande", () => {
  expect(currentSection(order, new Set(["fin-de-support"]), "fin-de-support", true)).toBe("agent-ia");
});

test("une section absente de la page (lecture en échec) n'est jamais choisie", () => {
  const present = ["acces", "agent-ia"];
  expect(currentSection(present, new Set(["alertes"]), "acces", false)).toBe("acces");
});
