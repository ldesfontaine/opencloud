import type { Page } from "@playwright/test";

import { label } from "./support/catalog";
import { expect, test } from "@playwright/test";

// Les captures de L2 (docs/projet/refonte-ux/captures/L2-*.png) : l'onglet des
// unités systemd d'une machine (puis son menu de vue ouvert), l'assistant
// ouvert sur une unité relevée avec le port visible, la fiche d'un binaire sans
// port, la fiche d'un binaire que la machine ne connaît pas, et le choix de
// « Déclarer un service » qui mène aux unités. Données fictives seulement ; rien
// n'est déclaré (les fenêtres s'ouvrent, aucune n'est validée). Hors `make ci` :
// `E2E_CAPTURES=1 npm run e2e`.
test.skip(process.env.E2E_CAPTURES !== "1", "captures demandées par E2E_CAPTURES=1");

const folder = new URL("../../docs/projet/refonte-ux/captures/", import.meta.url);

async function shoot(page: Page, name: string, theme: "light" | "dark"): Promise<void> {
  await page.screenshot({ path: new URL(`L2-${name}-${theme === "light" ? "clair" : "sombre"}.png`, folder).pathname });
}

async function openUnits(page: Page): Promise<void> {
  await page.goto("/machines");
  await page.getByRole("link", { name: "vps-paris-1", exact: true }).first().click();
  await page.getByRole("link", { name: label("module.systemd_units"), exact: true }).click();
  await expect(page.locator("main table tbody tr")).toHaveCount(6);
}

for (const theme of ["light", "dark"] as const) {
  test.describe(theme, () => {
    test.beforeEach(async ({ page }) => {
      await page.addInitScript((value) => localStorage.setItem("theme", value), theme);
      await page.setViewportSize({ width: 1280, height: 1000 });
    });

    test(`unités de la machine ${theme}`, async ({ page }) => {
      await openUnits(page);
      await shoot(page, "unites-machine", theme);
    });

    test(`menu de vue ouvert ${theme}`, async ({ page }) => {
      await openUnits(page);
      await page.locator("#systemd-view").click();
      await expect(page.locator("[role=listbox] [role=option]").filter({ hasText: label("systemd.view_failed") })).toBeVisible();
      await shoot(page, "unites-menu-vue", theme);
    });

    test(`assistant sur une unité relevée ${theme}`, async ({ page }) => {
      await openUnits(page);
      await page.locator("main table tbody tr", { hasText: "ssh.service" }).getByRole("link", { name: label("systemd.declare") }).click();
      const dialog = page.locator("dialog[open]");
      await expect(dialog.locator("#service-name")).toHaveValue("ssh");
      await dialog.getByRole("button", { name: label("wizard.next"), exact: true }).click();
      await expect(dialog.locator("#unit-0-ports")).toBeVisible();
      await dialog.locator("#unit-0-ports").fill("22");
      await shoot(page, "declarer-unite-port", theme);
    });

    test(`fiche d'un binaire sans port ${theme}`, async ({ page }) => {
      await openUnits(page);
      await page.locator("main table tbody tr", { hasText: "forgejo.service" }).getByRole("link", { name: "forgejo", exact: true }).click();
      await expect(page.locator("main")).toContainText(label("directory.port_to_declare"));
      await shoot(page, "fiche-binaire-sans-port", theme);
    });

    test(`fiche d'un binaire absent ${theme}`, async ({ page }) => {
      await page.goto("/services");
      await page.locator("main").getByRole("link", { name: "caddy", exact: true }).first().click();
      await expect(page.locator("main")).toContainText(label("systemd.presence_missing"));
      await shoot(page, "fiche-binaire-absent", theme);
    });

    test(`choix de la déclaration ${theme}`, async ({ page }) => {
      await page.goto("/services/annuaire/nouveau");
      await expect(page.locator("dialog[open]").getByText(label("directory.pick_units"))).toBeVisible();
      await shoot(page, "declarer-choix-unites", theme);
    });
  });
}
