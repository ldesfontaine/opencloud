import { expect, test } from "./support/findings";
import { exerciseSelects } from "./support/selects";

// Le défaut d'origine : la liste d'un menu déroulant portée dans <body> restait
// sous la couche supérieure du <dialog> modal, inerte, donc invisible et
// inclicable. Ces fenêtres portent un menu déroulant chacune.
const windows = [
  { name: "l'assistant de sonde (Type)", url: "/disponibilite/nouvelle" },
  { name: "Ajouter un plan (Source)", url: "/sauvegardes/plans/nouveau" },
  { name: "le canal de notification (Format)", url: "/parametres/canaux/nouveau" },
];

for (const window of windows) {
  test(`dans ${window.name}, les menus déroulants s'ouvrent et se choisissent`, async ({ page, findings }) => {
    findings.where = window.url;
    await page.goto(window.url);
    const dialog = page.locator("dialog[open]");
    await expect(dialog).toBeVisible();
    const count = await exerciseSelects(page, dialog, findings);
    expect(count).toBeGreaterThan(0);
    expect(findings.unknown()).toEqual([]);
  });
}
