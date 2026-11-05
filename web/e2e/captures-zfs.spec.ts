import { expect, test, type Page } from "@playwright/test";

import { label } from "./support/catalog";

// Les captures du cache ZFS (docs/projet/refonte-ux/captures/ZFS-*.png) : la
// tuile mémoire d'un Pi en ZFS, puis la même fiche avec la fenêtre « Ré-enrôler »
// ouverte. Données fictives seulement. Hors `make ci` : `E2E_CAPTURES=1 npm run e2e`.
test.skip(process.env.E2E_CAPTURES !== "1", "captures demandées par E2E_CAPTURES=1");

const folder = new URL("../../docs/projet/refonte-ux/captures/", import.meta.url);
const zfsMachine = "aaaaaaaa-1111-4222-8333-444444444444";

async function open(page: Page): Promise<void> {
  await page.setViewportSize({ width: 1500, height: 900 });
  await page.clock.setFixedTime(new Date("2026-09-12T12:00:00Z"));
  await page.addInitScript(() => localStorage.setItem("theme", "light"));
  await page.goto(`/machines/${zfsMachine}`);
  await expect(page.locator(".resource-stats")).toContainText(/ZFS/);
}

async function shoot(page: Page, name: string): Promise<void> {
  await page.waitForTimeout(250);
  await page.screenshot({ path: new URL(`ZFS-${name}-clair.png`, folder).pathname });
}

test("la fiche : mémoire corrigée et cache ZFS à part", async ({ page }) => {
  await open(page);
  await shoot(page, "fiche");
});

test("la fiche avec la fenêtre Ré-enrôler ouverte", async ({ page }) => {
  await open(page);
  await page.getByRole("link", { name: label("machine.reenroll") }).first().click();
  await expect(page.locator("dialog[open]")).toBeVisible();
  await shoot(page, "fenetre");
});
