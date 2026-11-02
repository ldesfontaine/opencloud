import type { Page } from "@playwright/test";

import { label } from "./support/catalog";
import { expect, test } from "@playwright/test";

// Les captures de L6 (docs/projet/refonte-ux/captures/L6-*.png) : la fiche du
// service « dashboard » (trois connexions vues sur son port, preuve
// « Observé »), et la fenêtre « Ajouter un lien » avec son menu ouvert (des services, jamais une source). Rien ne s'écrit. Données
// fictives seulement. Hors `make ci` : `E2E_CAPTURES=1 npm run e2e`.
test.skip(process.env.E2E_CAPTURES !== "1", "captures demandées par E2E_CAPTURES=1");

const folder = new URL("../../docs/projet/refonte-ux/captures/", import.meta.url);

async function shoot(page: Page, name: string, theme: "light" | "dark"): Promise<void> {
  await page.screenshot({ path: new URL(`L6-${name}-${theme === "light" ? "clair" : "sombre"}.png`, folder).pathname });
}

async function openService(page: Page, name: string): Promise<void> {
  await page.goto("/services");
  await page.locator("main").getByRole("link", { name: new RegExp(`^${name}(\\s|$)`) }).first().click();
  await expect(page.getByRole("heading", { name, exact: true })).toBeVisible();
  await expect(page.locator("main section", { has: page.getByText(label("link.title"), { exact: true }) }).locator("tbody tr").first()).toBeVisible();
}

for (const theme of ["light", "dark"] as const) {
  test.describe(theme, () => {
    test.beforeEach(async ({ page }) => {
      await page.addInitScript((value) => localStorage.setItem("theme", value), theme);
      await page.setViewportSize({ width: 1280, height: 900 });
      await page.clock.setFixedTime(new Date("2026-09-12T12:00:00Z"));
    });

    test(`fiche d'un service, connexions vues ${theme}`, async ({ page }) => {
      await openService(page, "dashboard");
      await expect(page.getByText(label("link.source_internet"), { exact: true })).toBeVisible();
      await page.locator("main section", { has: page.getByText(label("link.title"), { exact: true }) }).evaluate((card) => card.scrollIntoView({ block: "start" }));
      await shoot(page, "fiche-connexions", theme);
    });

    test(`fenêtre Ajouter un lien depuis le service, menu ouvert ${theme}`, async ({ page }) => {
      await openService(page, "dashboard");
      await page.getByRole("link", { name: label("link.add"), exact: true }).click();
      const dialog = page.locator("dialog[open]");
      await dialog.locator("#link-peer").click();
      await expect(page.locator("[role=listbox] [role=option]").filter({ hasText: "notes" }).first()).toBeVisible();
      await shoot(page, "ajouter-menu-service", theme);
    });
  });
}
