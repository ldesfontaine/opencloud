import { mkdirSync } from "node:fs";

import type { Page } from "@playwright/test";

import { label } from "./support/catalog";
import { expect, test } from "./support/findings";

// Les catégories d'un canal (#494), sur les données fictives : Paramètres ›
// Notifications › un canal, sous « Réglages ». Sans case cochée, le canal reçoit
// toutes les alertes ; cocher des familles le restreint, et la liste des canaux
// le dit. Chaque écran touché est cliqué : la fenêtre du canal (création,
// modification) et la liste. Avec E2E_CAPTURES=1, il laisse les captures CC-*.

const captureFolder = new URL("../../docs/projet/refonte-ux/captures/", import.meta.url).pathname;
const capturing = process.env.E2E_CAPTURES === "1";

async function shoot(page: Page, name: string): Promise<void> {
  if (!capturing) {
    return;
  }
  mkdirSync(captureFolder, { recursive: true });
  await page.waitForTimeout(250);
  await page.screenshot({ path: `${captureFolder}CC-${name}-clair.png` });
}

test.beforeEach(async ({ page }) => {
  await page.setViewportSize({ width: 1500, height: 1000 });
  await page.addInitScript(() => localStorage.setItem("theme", "light"));
});

test("un canal sans catégorie reçoit tout ; cocher des familles le restreint, la liste et la fenêtre le disent", async ({ page, findings }) => {
  findings.where = "/parametres/canaux/nouveau";
  await page.goto("/parametres/canaux/nouveau");
  const dialog = page.locator("dialog[open]");
  const settings = dialog.locator("details.fold").first();
  const hint = settings.locator(":scope > summary .fold-hint");

  // État vide : aucune case, toutes les alertes.
  await expect(hint).toContainText(label("channel.hint_categories_all"));
  await settings.locator(":scope > summary").click();
  await expect(dialog.getByText(label("channel.categories_label"), { exact: true })).toBeVisible();
  for (const category of ["machines", "services", "jobs", "probes", "backups", "updates"]) {
    await expect(dialog.getByLabel(label(`channel.category_${category}`))).not.toBeChecked();
  }

  await dialog.locator("#channel-name").fill("salon astreinte cc");
  await dialog.locator("#channel-url").fill("https://hooks.exemple.fr/cc");
  await dialog.getByLabel(label("channel.category_updates")).check();
  await dialog.getByLabel(label("channel.category_backups")).check();
  await expect(hint).toContainText(`${label("channel.category_short_backups")}, ${label("channel.category_short_updates")}`);
  await shoot(page, "fenetre-ouverte");

  await dialog.getByRole("button", { name: label("channel.create"), exact: true }).click();
  await expect(page).toHaveURL(/\/parametres$/);
  const row = page.locator("tr").filter({ hasText: "salon astreinte cc" });
  await expect(row).toContainText(label("channel.category_short_updates"));
  await expect(row).toContainText(label("channel.category_short_backups"));
  await shoot(page, "liste");

  // Relu après enregistrement, puis ramené à « toutes » en décochant tout.
  await row.getByRole("link", { name: "salon astreinte cc" }).click();
  const again = page.locator("dialog[open]");
  await again.locator("details.fold > summary").first().click();
  await expect(again.getByLabel(label("channel.category_updates"))).toBeChecked();
  await expect(again.getByLabel(label("channel.category_machines"))).not.toBeChecked();
  await again.getByLabel(label("channel.category_updates")).uncheck();
  await again.getByLabel(label("channel.category_backups")).uncheck();
  await again.getByRole("button", { name: label("channel.save"), exact: true }).click();
  await expect(page).toHaveURL(/\/parametres$/);
  const all = page.locator("tr").filter({ hasText: "salon astreinte cc" });
  await expect(all).not.toContainText(label("channel.category_short_updates"));
  expect(findings.unknown()).toEqual([]);
});

test("erreur de lecture : le canal qui ne se lit pas le dit, sans cases trompeuses", async ({ page, findings }) => {
  findings.where = "/parametres/canaux/1";
  await page.route("**/api/alerts/channels/*", (route) => route.fulfill({ status: 500, json: { error: "internal" } }));
  await page.goto("/parametres/canaux/1");
  const dialog = page.locator("dialog[open]");
  await expect(dialog).toContainText(label("error.internal"));
  await expect(dialog.getByLabel(label("channel.category_updates"))).toHaveCount(0);
  await shoot(page, "erreur");
  findings.all.splice(0, findings.all.length, ...findings.all.filter((finding) => !/alerts\/channels\/1 répond 500/.test(finding.what)));
  expect(findings.unknown()).toEqual([]);
});
