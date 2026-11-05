import { mkdirSync } from "node:fs";

import type { Page } from "@playwright/test";

import { label } from "./support/catalog";
import { expect, test } from "./support/findings";

// Les alertes de ressources (#489), sur les données fictives : la machine
// distante est restée à 100 % de processeur sur plus d'une fenêtre (une alerte
// de danger, non lue) et sa mémoire a des seuils à elle ; la machine openCloud
// suit partout le réglage global. Chaque écran touché est cliqué : la boîte
// d'alertes (« Lu / Non lu »), Paramètres › Notifications (seuils et rappel), la
// fiche d'une machine (sa surcharge), la fenêtre « Faire taire » ouverte depuis
// l'alerte. Avec E2E_CAPTURES=1, il laisse les captures SE-*.

const captureFolder = new URL("../../docs/projet/refonte-ux/captures/", import.meta.url).pathname;
const capturing = process.env.E2E_CAPTURES === "1";

async function shoot(page: Page, name: string): Promise<void> {
  if (!capturing) {
    return;
  }
  mkdirSync(captureFolder, { recursive: true });
  await page.waitForTimeout(250);
  await page.screenshot({ path: `${captureFolder}SE-${name}-clair.png` });
}

async function remoteMachineID(page: Page): Promise<string> {
  const answer = (await (await page.request.get("/api/machines")).json()) as { machines: { id: string; name: string }[] };
  const found = answer.machines.find((machine) => machine.name === "vps-paris-1");
  if (found === undefined) {
    throw new Error("la machine distante des données fictives manque");
  }
  return found.id;
}

function tile(page: Page, resource: "cpu" | "memory" | "disk") {
  return page.locator("section.threshold-tile").filter({ has: page.getByText(label(`thresholds.resource_${resource}`), { exact: true }) });
}

test.beforeEach(async ({ page }) => {
  await page.setViewportSize({ width: 1500, height: 1000 });
  await page.addInitScript(() => localStorage.setItem("theme", "light"));
});

test("la boîte d'alertes dit « Lue » et « Non lue », jamais « acquittée », et l'alerte de processeur se lit", async ({ page, findings }) => {
  findings.where = "/alertes";
  await page.goto("/alertes");
  const main = page.locator("main");
  await expect(main.getByText(label("alerts.section_unseen"), { exact: false }).first()).toBeVisible();
  await expect(main).not.toContainText(/acquitt/i);
  await expect(main).not.toContainText(/\bvues?\b/i);

  const row = main.locator(".alert-row, li, article").filter({ hasText: /Processeur à \d+ % en moyenne/ }).first();
  await expect(row).toBeVisible();
  await expect(row).toContainText(/moyenne sur 15 min/);
  await shoot(page, "alertes");

  // « Faire taire » ouvre la fenêtre, avec le type de l'alerte pré-rempli ;
  // un silence qui la couvre empêche son rappel.
  await row.getByRole("link", { name: label("alerts.silence") }).click();
  const dialog = page.locator("dialog[open]");
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText(label("alert.kind_cpu_high"));
  await shoot(page, "silence");
  await dialog.getByRole("button", { name: label("modal.close") }).first().click();
  await expect(dialog).toHaveCount(0);

  // Marquer comme lue : l'alerte passe sous « Lues », sa pastille dit « Lue ».
  // La fenêtre ayant mené à Paramètres, on revient à la boîte.
  await page.goto("/alertes");
  const unread = main.locator(".alert-row, li, article").filter({ hasText: /Processeur à \d+ % en moyenne/ }).first();
  await unread.getByRole("button", { name: label("alert.acknowledge") }).click();
  await expect(unread.getByRole("button", { name: label("alert.acknowledge") })).toHaveCount(0);
  await expect(main.getByText(label("alerts.section_seen"), { exact: false }).first()).toBeVisible();
  expect(findings.unknown()).toEqual([]);
});

test("Paramètres › Notifications : un formulaire par ressource, la fenêtre du disque absente, un refus lisible, le rappel", async ({ page, findings }) => {
  findings.where = "/parametres";
  await page.goto("/parametres#notifications");
  const section = page.locator("#notifications");
  await expect(section.getByText(label("settings.thresholds_title"))).toBeVisible();
  for (const resource of ["cpu", "memory", "disk"] as const) {
    await expect(section.getByRole("heading", { name: label(`thresholds.resource_${resource}`) })).toBeVisible();
  }
  // Le disque est jugé sur la dernière mesure : pas de champ de fenêtre.
  await expect(section.locator("#threshold-disk-window")).toHaveCount(0);
  await expect(section.locator("#threshold-disk-attention")).toHaveValue("85");
  await expect(section.locator("#threshold-cpu-attention")).toHaveValue("95");
  await expect(section.locator("#threshold-cpu-danger")).toHaveValue("99");
  await expect(section.locator("#threshold-cpu-window")).toHaveValue("15");
  await expect(section.locator("#threshold-memory-attention")).toHaveValue("90");

  // Un refus joué dans le navigateur, sans écriture : l'attention au-dessus du danger.
  const cpu = section.locator("section.threshold-section").filter({ has: page.getByRole("heading", { name: label("thresholds.resource_cpu") }) });
  await section.locator("#threshold-cpu-attention").fill("99");
  await section.locator("#threshold-cpu-danger").fill("95");
  await cpu.getByRole("button", { name: label("thresholds.save") }).click();
  await expect(cpu).toContainText(label("settings.threshold_invalid"));

  // Une règle valable s'enregistre, la pastille le dit ; puis on remet celle du produit.
  await section.locator("#threshold-cpu-attention").fill("96");
  await section.locator("#threshold-cpu-danger").fill("99");
  await section.locator("#threshold-cpu-window").fill("20");
  await cpu.getByRole("button", { name: label("thresholds.save") }).click();
  await expect(cpu.getByText(label("thresholds.saved"))).toBeVisible();
  await section.locator("#threshold-cpu-attention").fill("95");
  await section.locator("#threshold-cpu-window").fill("15");
  await cpu.getByRole("button", { name: label("thresholds.save") }).click();
  await expect(cpu.getByText(label("thresholds.saved"))).toBeVisible();

  // Le rappel : 12 h par défaut, 0 le coupe, une valeur hors bornes est refusée.
  const reminder = section.locator("#reminder-hours");
  await expect(reminder).toHaveValue("12");
  await reminder.scrollIntoViewIfNeeded();
  await shoot(page, "parametres");
  await reminder.fill("0");
  await section.getByRole("button", { name: label("thresholds.save") }).last().click();
  await expect(section.getByText(label("settings.reminder_off"))).toBeVisible();
  // Hors bornes, le champ lui-même refuse (le serveur aussi, voir les tests Go).
  await reminder.fill("999");
  expect(await reminder.evaluate((field: HTMLInputElement) => field.validity.rangeOverflow)).toBe(true);
  await reminder.fill("12");
  await section.getByRole("button", { name: label("thresholds.save") }).last().click();
  await expect(section.getByText(/Rappel après 12 h/)).toBeVisible();
  expect(findings.unknown()).toEqual([]);
});

test("fiche d'une machine : la règle en vigueur et d'où elle vient, une surcharge posée puis retirée", async ({ page, findings }) => {
  const id = await remoteMachineID(page);
  findings.where = `/machines/${id}`;
  await page.goto(`/machines/${id}`);
  const card = page.locator(".card").filter({ has: page.getByText(label("thresholds.machine_title"), { exact: true }) });
  await expect(card).toBeVisible();

  // La mémoire porte ses seuils ; le processeur et le disque suivent le global.
  await expect(tile(page, "memory")).toContainText(label("thresholds.origin_own"));
  await expect(tile(page, "memory")).toContainText("96");
  await expect(tile(page, "memory")).toContainText(/Réglage global : Attention à 90 %/);
  await expect(tile(page, "cpu")).toContainText(label("thresholds.origin_global"));
  await expect(tile(page, "disk")).toContainText(label("thresholds.origin_global"));
  await expect(card).not.toContainText(label("thresholds.machine_all_follow"));

  // Plus de tolérance pour le processeur de cette machine.
  await tile(page, "cpu").getByRole("button", { name: label("thresholds.more_tolerance") }).click();
  await expect(page.locator("#machine-threshold-cpu-attention")).toHaveValue("95");
  await page.locator("#machine-threshold-cpu-attention").fill("98");
  await page.locator("#machine-threshold-cpu-danger").fill("100");
  await page.locator("#machine-threshold-cpu-window").fill("30");
  await card.scrollIntoViewIfNeeded();
  await page.evaluate(() => window.scrollBy(0, -16));
  await shoot(page, "fiche");
  await tile(page, "cpu").getByRole("button", { name: label("thresholds.save_machine") }).click();
  await expect(tile(page, "cpu")).toContainText(label("thresholds.origin_own"));
  await expect(tile(page, "cpu")).toContainText(/Attention à 98 %, danger à 100 %, moyenne sur 30 min/);

  // Annuler une édition ne change rien.
  await tile(page, "disk").getByRole("button", { name: label("thresholds.more_tolerance") }).click();
  await tile(page, "disk").getByRole("button", { name: label("thresholds.cancel") }).click();
  await expect(tile(page, "disk")).toContainText(label("thresholds.origin_global"));

  // Le suivre de nouveau.
  await tile(page, "cpu").getByRole("button", { name: label("thresholds.follow_global") }).click();
  await expect(tile(page, "cpu")).toContainText(label("thresholds.origin_global"));
  await expect(tile(page, "cpu")).toContainText(/Attention à 95 %, danger à 99 %, moyenne sur 15 min/);
  expect(findings.unknown()).toEqual([]);
});

test("fiche d'une machine : l'état vide (rien de propre), puis l'erreur de lecture", async ({ page, findings }) => {
  findings.where = "/machines/local";
  await page.goto("/machines/local");
  const card = page.locator(".card").filter({ has: page.getByText(label("thresholds.machine_title"), { exact: true }) });
  // Vide : aucune ressource n'a de seuils à elle, la carte le dit et propose d'en donner.
  await expect(card).toContainText(label("thresholds.machine_all_follow"));
  await expect(card.getByText(label("thresholds.origin_own"))).toHaveCount(0);
  await expect(card.getByRole("button", { name: label("thresholds.more_tolerance") })).toHaveCount(3);

  // Erreur de lecture : la carte dit l'erreur, sans tuile ni état vide trompeur.
  await page.route("**/api/machines/*/thresholds", (route) => route.fulfill({ status: 500, json: { error: "internal" } }));
  await page.goto("/machines/local");
  const failing = page.locator(".card").filter({ has: page.getByText(label("thresholds.machine_title"), { exact: true }) });
  await expect(failing).toContainText(label("error.internal"));
  await expect(failing).not.toContainText(label("thresholds.machine_all_follow"));
  await expect(failing.locator("section.threshold-tile")).toHaveCount(0);
  findings.all.splice(0, findings.all.length, ...findings.all.filter((finding) => !/thresholds répond 500/.test(finding.what)));
  expect(findings.unknown()).toEqual([]);
});
