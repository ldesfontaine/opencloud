import type { Page } from "@playwright/test";

import { label } from "./support/catalog";
import { expect, test } from "@playwright/test";

// Les captures de L4 (docs/projet/refonte-ux/captures/L4-*.png) : la fiche du
// service « blog » avec sa route déduite à confirmer ou écarter, la fiche du
// proxy qui en porte deux, la route confirmée, la route écartée rangée sous
// « Écartés » (pli ouvert), et la fenêtre « Ajouter un lien » ouverte depuis le
// proxy, menu ouvert. Chaque geste qui écrit se défait dans le même test (la
// route confirmée est retirée, l'écartée rétablie) : les deux thèmes partent
// du même état. Données fictives seulement. Hors `make ci` :
// `E2E_CAPTURES=1 npm run e2e`.
test.skip(process.env.E2E_CAPTURES !== "1", "captures demandées par E2E_CAPTURES=1");

const folder = new URL("../../docs/projet/refonte-ux/captures/", import.meta.url);

async function shoot(page: Page, name: string, theme: "light" | "dark"): Promise<void> {
  await page.screenshot({ path: new URL(`L4-${name}-${theme === "light" ? "clair" : "sombre"}.png`, folder).pathname });
}

async function openService(page: Page, name: string): Promise<void> {
  await page.goto("/services");
  await page.locator("main").getByRole("link", { name: new RegExp(`^${name}(\\s|$)`) }).first().click();
  await expect(page.getByRole("heading", { name, exact: true })).toBeVisible();
  await expect(page.locator("main section", { has: page.getByText(label("link.title"), { exact: true }) }).locator("tbody tr").first()).toBeVisible();
}

const dismissedFold = (page: Page) => page.locator("summary", { hasText: label("link.dismissed_title").replace("%s", "1") });

for (const theme of ["light", "dark"] as const) {
  test.describe(theme, () => {
    test.beforeEach(async ({ page }) => {
      await page.addInitScript((value) => localStorage.setItem("theme", value), theme);
      await page.setViewportSize({ width: 1280, height: 900 });
      await page.clock.setFixedTime(new Date("2026-09-12T12:00:00Z"));
    });

    test(`route déduite à confirmer ou écarter ${theme}`, async ({ page }) => {
      await openService(page, "blog");
      await page.getByText(label("link.title"), { exact: true }).scrollIntoViewIfNeeded();
      await shoot(page, "fiche-deduit", theme);
    });

    test(`fiche du proxy, deux routes déduites ${theme}`, async ({ page }) => {
      await openService(page, "caddy");
      await page.getByText(label("link.title"), { exact: true }).scrollIntoViewIfNeeded();
      await shoot(page, "fiche-proxy", theme);
    });

    test(`route confirmée ${theme}`, async ({ page }) => {
      await openService(page, "blog");
      const row = page.locator("main tbody tr", { hasText: "caddy" });
      await row.getByRole("button", { name: label("link.confirm"), exact: true }).click();
      await expect(row.getByRole("button", { name: label("link.remove"), exact: true })).toBeVisible();
      await page.getByText(label("link.title"), { exact: true }).scrollIntoViewIfNeeded();
      await shoot(page, "fiche-confirme", theme);
      await row.getByRole("button", { name: label("link.remove"), exact: true }).click();
      await page.getByRole("button", { name: label("confirm.yes"), exact: true }).click();
      await expect(row.getByRole("button", { name: label("link.confirm"), exact: true })).toBeVisible();
    });

    test(`route écartée, pli ouvert ${theme}`, async ({ page }) => {
      await openService(page, "wiki");
      const row = page.locator("main tbody tr", { hasText: "caddy" });
      await row.getByRole("button", { name: label("link.dismiss"), exact: true }).click();
      await expect(dismissedFold(page)).toBeVisible();
      await dismissedFold(page).click();
      await expect(row.getByRole("button", { name: label("link.restore"), exact: true })).toBeVisible();
      await page.getByText(label("link.title"), { exact: true }).scrollIntoViewIfNeeded();
      await shoot(page, "fiche-ecarte-pli", theme);
      await row.getByRole("button", { name: label("link.restore"), exact: true }).click();
      await expect(page.locator("main tbody tr", { hasText: "caddy" }).getByRole("button", { name: label("link.confirm"), exact: true })).toBeVisible();
    });

    test(`fenêtre Ajouter un lien depuis le proxy, menu ouvert ${theme}`, async ({ page }) => {
      await openService(page, "caddy");
      await page.getByRole("link", { name: label("link.add"), exact: true }).click();
      const dialog = page.locator("dialog[open]");
      await dialog.locator("#link-peer").click();
      await expect(page.locator("[role=listbox] [role=option]").filter({ hasText: "blog" })).toBeVisible();
      await shoot(page, "ajouter-menu-proxy", theme);
    });
  });
}
