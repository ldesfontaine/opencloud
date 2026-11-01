import type { Page } from "@playwright/test";

import { label } from "./support/catalog";
import { expect, test } from "@playwright/test";

// Les captures de L3 (docs/projet/refonte-ux/captures/L3-*.png) : la fiche d'un
// service avec sa carte Liens (preuve, fraîcheur, lien ancien), la fenêtre
// « Ajouter un lien » avec un menu ouvert, la question avant de retirer un
// lien, la carte Réseau de la vue d'ensemble qui trace les liens, et
// l'inspecteur de l'onglet Réseau d'une machine. Données fictives seulement ;
// rien n'est écrit (les fenêtres s'ouvrent, aucune n'est validée). Hors
// `make ci` : `E2E_CAPTURES=1 npm run e2e`.
test.skip(process.env.E2E_CAPTURES !== "1", "captures demandées par E2E_CAPTURES=1");

const folder = new URL("../../docs/projet/refonte-ux/captures/", import.meta.url);

async function shoot(page: Page, name: string, theme: "light" | "dark"): Promise<void> {
  await page.screenshot({ path: new URL(`L3-${name}-${theme === "light" ? "clair" : "sombre"}.png`, folder).pathname });
}

async function openNextcloud(page: Page): Promise<void> {
  await page.goto("/services");
  await page.locator("main").getByRole("link", { name: /^nextcloud(\s|$)/ }).first().click();
  await expect(page.getByRole("heading", { name: "nextcloud", exact: true })).toBeVisible();
  await expect(page.locator("main section", { has: page.getByText(label("link.title"), { exact: true }) }).locator("tbody tr")).toHaveCount(3);
}

for (const theme of ["light", "dark"] as const) {
  test.describe(theme, () => {
    test.beforeEach(async ({ page }) => {
      await page.addInitScript((value) => localStorage.setItem("theme", value), theme);
      await page.setViewportSize({ width: 1280, height: 1000 });
    });

    test(`fiche d'un service, carte Liens ${theme}`, async ({ page }) => {
      await openNextcloud(page);
      await page.getByText(label("link.title"), { exact: true }).scrollIntoViewIfNeeded();
      await shoot(page, "fiche-liens", theme);
    });

    test(`fenêtre Ajouter un lien, menu ouvert ${theme}`, async ({ page }) => {
      await openNextcloud(page);
      await page.getByRole("link", { name: label("link.add"), exact: true }).click();
      const dialog = page.locator("dialog[open]");
      await dialog.locator("#link-peer").click();
      await expect(page.locator("[role=listbox] [role=option]").filter({ hasText: "caddy" })).toBeVisible();
      await shoot(page, "ajouter-menu-service", theme);
    });

    test(`fenêtre Ajouter un lien, remplie ${theme}`, async ({ page }) => {
      await openNextcloud(page);
      await page.getByRole("link", { name: label("link.add"), exact: true }).click();
      const dialog = page.locator("dialog[open]");
      await dialog.locator("#link-peer").click();
      await page.locator("[role=listbox] [role=option]").filter({ hasText: "cloud-db" }).click();
      await dialog.locator("#link-self-unit").click();
      await page.locator("[role=listbox] [role=option]").filter({ hasText: "redis" }).click();
      await dialog.locator("#link-port").fill("5432");
      await shoot(page, "ajouter-rempli", theme);
    });

    test(`question avant de retirer un lien ${theme}`, async ({ page }) => {
      await openNextcloud(page);
      const mine = page.locator("main tbody tr", { hasText: "caddy" });
      await mine.getByRole("button", { name: label("link.remove"), exact: true }).click();
      await expect(page.getByText(label("link.remove_confirm").replace("%s", "caddy"))).toBeVisible();
      await shoot(page, "retirer-question", theme);
    });

    test(`réseau de la vue d'ensemble ${theme}`, async ({ page }) => {
      await page.goto("/");
      await expect(page.getByText(/lien.* entre vos services/)).toBeVisible();
      await page.getByText(/lien.* entre vos services/).scrollIntoViewIfNeeded();
      await shoot(page, "reseau-vue-ensemble", theme);
    });

    test(`onglet Réseau d'une machine ${theme}`, async ({ page }) => {
      await page.goto("/machines");
      await page.getByRole("link", { name: "vps-paris-1", exact: true }).first().click();
      await page.getByRole("link", { name: label("tab.network"), exact: true }).click();
      await page.locator(".netgraph-node", { hasText: "nextcloud" }).first().click();
      await expect(page.locator(".inspector")).toContainText(label("link.proof_declared"));
      await shoot(page, "reseau-machine", theme);
    });
  });
}
