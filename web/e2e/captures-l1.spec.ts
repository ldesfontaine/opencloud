import type { Page } from "@playwright/test";

import { label } from "./support/catalog";
import { expect, test } from "@playwright/test";

// Les captures de L1 (docs/projet/refonte-ux/captures/L1-*.png) : la liste des
// services rangée par projet Compose avec un service plié sur deux machines, le
// choix d'un conteneur détecté (menu ouvert), l'assistant rempli par ce choix,
// l'étape Projets avec « Créer un projet ici », et la fiche d'un service qui dit
// ce qu'il est. Données fictives seulement ; rien n'est déclaré (les fenêtres
// s'ouvrent, aucune n'est validée). Hors `make ci` : `E2E_CAPTURES=1 npm run e2e`.
test.skip(process.env.E2E_CAPTURES !== "1", "captures demandées par E2E_CAPTURES=1");

const folder = new URL("../../docs/projet/refonte-ux/captures/", import.meta.url);

async function shoot(page: Page, name: string, theme: "light" | "dark"): Promise<void> {
  await page.screenshot({ path: new URL(`L1-${name}-${theme === "light" ? "clair" : "sombre"}.png`, folder).pathname });
}

for (const theme of ["light", "dark"] as const) {
  test.describe(theme, () => {
    test.beforeEach(async ({ page }) => {
      await page.addInitScript((value) => localStorage.setItem("theme", value), theme);
      await page.setViewportSize({ width: 1280, height: 1100 });
    });

    test(`liste des services ${theme}`, async ({ page }) => {
      await page.goto("/services");
      const table = page.locator("main table:has(tr.svc-project)");
      await expect(table.locator("tr.svc-project", { hasText: "edge" })).toBeVisible();
      await table.getByRole("button", { name: "Déplier les machines de traefik" }).click();
      await expect(table.locator("tr.svc-instance")).toHaveCount(2);
      await shoot(page, "liste-services", theme);
    });

    test(`choisir un conteneur ${theme}`, async ({ page }) => {
      await page.goto("/services/annuaire/nouveau");
      const dialog = page.locator("dialog[open]");
      await dialog.locator("#service-pick").click();
      await expect(page.locator("[role=listbox] [role=option]").filter({ hasText: "authelia (edge)" })).toBeVisible();
      await shoot(page, "declarer-choix-menu", theme);
    });

    test(`assistant rempli ${theme}`, async ({ page }) => {
      await page.goto("/services/annuaire/nouveau");
      const dialog = page.locator("dialog[open]");
      await dialog.locator("#service-pick").click();
      await page.locator("[role=listbox] [role=option]").filter({ hasText: "authelia (edge)" }).click();
      await expect(dialog.locator("#service-name")).toHaveValue("authelia");
      await dialog.locator("#service-category").click();
      await expect(page.locator("[role=listbox] [role=option]").filter({ hasText: label("directory.category_security") })).toBeVisible();
      await shoot(page, "declarer-identite-type", theme);
    });

    test(`etape projets ${theme}`, async ({ page }) => {
      await page.goto("/services/annuaire/nouveau");
      const dialog = page.locator("dialog[open]");
      await dialog.getByRole("button", { name: label("directory.pick_manual"), exact: true }).click();
      await dialog.locator("#service-name").fill("mon-service");
      await dialog.locator("#service-compose").fill("edge");
      await dialog.getByRole("button", { name: label("wizard.next"), exact: true }).click();
      await dialog.locator("#unit-0-name").fill("mon-service");
      await dialog.locator("#unit-0-image").fill("nginx");
      await dialog.getByRole("button", { name: label("wizard.next"), exact: true }).click();
      await expect(dialog.getByText(label("directory.projects_optional"))).toBeVisible();
      await shoot(page, "declarer-projets", theme);
    });

    test(`fiche d'un service ${theme}`, async ({ page }) => {
      await page.goto("/services");
      await page.locator("main table:has(tr.svc-project)").getByRole("link", { name: "nextcloud" }).first().click();
      await expect(page.locator("main")).toContainText(label("directory.field_category"));
      await shoot(page, "fiche-service", theme);
    });
  });
}
