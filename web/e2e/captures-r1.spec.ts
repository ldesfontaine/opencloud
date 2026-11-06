import { expect, test, type Page } from "@playwright/test";

import { label } from "./support/catalog";
import { bigMap, demoMap, flatMap } from "./support/netmap";
import type { NetworkMap } from "../src/api/network";

// Les captures de R1 (docs/projet/refonte-ux/captures/R1-*.png) : la carte
// Réseau de la vue d'ensemble. La carte réelle vient du serveur de données
// fictives ; les autres (la forme d'un vrai réseau, tout à plat, quarante
// machines) sont les réponses figées par le serveur (internal/server/testdata),
// servies par le parcours. Hors `make ci` : `E2E_CAPTURES=1 npm run e2e`.
test.skip(process.env.E2E_CAPTURES !== "1", "captures demandées par E2E_CAPTURES=1");

const folder = new URL("../../docs/projet/refonte-ux/captures/", import.meta.url);

async function shoot(page: Page, name: string, theme: "light" | "dark"): Promise<void> {
  await page.locator(".netmap-wrap").screenshot({ path: new URL(`R1-${name}-${theme === "light" ? "clair" : "sombre"}.png`, folder).pathname });
}

async function serve(page: Page, map: NetworkMap): Promise<void> {
  await page.route("**/api/network", (route) => route.fulfill({ json: map }));
}

async function openOverview(page: Page): Promise<void> {
  await page.goto("/");
  await expect(page.locator(".netmap")).toBeVisible();
  // Les traits se posent après la mesure des cartes.
  await expect(page.locator(".netmap-links .netmap-edge")).not.toHaveCount(0);
  await page.waitForTimeout(300);
}

for (const theme of ["light", "dark"] as const) {
  test.describe(theme, () => {
    test.beforeEach(async ({ page }) => {
      await page.addInitScript((value) => localStorage.setItem("theme", value), theme);
      await page.setViewportSize({ width: 1900, height: 1200 });
      await page.clock.setFixedTime(new Date("2026-09-12T12:00:00Z"));
    });

    test(`carte réelle du serveur ${theme}`, async ({ page }) => {
      await page.goto("/");
      await expect(page.locator(".netmap")).toBeVisible();
      await page.waitForTimeout(400);
      await shoot(page, "carte-reelle", theme);
    });

    test(`forme d'un vrai réseau, 1900 px ${theme}`, async ({ page }) => {
      await serve(page, demoMap());
      await openOverview(page);
      await shoot(page, "carte-complete", theme);
      if (theme === "light") {
        await page.screenshot({ path: new URL("R1-carte-complete-page-clair.png", folder).pathname, fullPage: true });
      }
    });

    test(`forme d'un vrai réseau, 1280 px ${theme}`, async ({ page }) => {
      await page.setViewportSize({ width: 1280, height: 900 });
      await serve(page, demoMap());
      await openOverview(page);
      await shoot(page, "carte-complete-1280", theme);
    });

    test(`hôte choisi (invités en évidence), zones sans machine dépliées ${theme}`, async ({ page }) => {
      await serve(page, demoMap());
      await openOverview(page);
      await page.getByRole("button", { name: label("network.map_empty_zones").replace("%s", "4") }).click();
      // Le clic fige la mise en évidence : la capture ne dépend pas de la souris.
      await page.getByRole("button", { name: label("network.map_hosts").replace("%s", "10") }).click();
      await page.mouse.move(0, 0);
      await page.waitForTimeout(300);
      await shoot(page, "carte-survol-hote", theme);
    });

    test(`survol du tunnel et liste des liens ${theme}`, async ({ page }) => {
      await serve(page, demoMap());
      await openOverview(page);
      await page.locator(".netmap-edge.tunnel").focus();
      await expect(page.locator(".netmap-tip")).toBeVisible();
      await shoot(page, "carte-survol-lien", theme);
      await page.mouse.move(0, 0);
      await page.locator(".netmap-wrap summary", { hasText: label("network.map_links_title").replace("%s", "4") }).click();
      await shoot(page, "carte-liste-liens", theme);
    });

    test(`carte à plat, sans Proxmox ni pare-feu ${theme}`, async ({ page }) => {
      await serve(page, flatMap());
      await openOverview(page);
      await shoot(page, "carte-a-plat", theme);
    });

    test(`carte de quarante machines, repliée par hôte ${theme}`, async ({ page }) => {
      await serve(page, bigMap());
      await page.goto("/");
      await expect(page.locator(".netmap")).toBeVisible();
      await page.waitForTimeout(300);
      await shoot(page, "carte-quarante-repliee", theme);
      await page.getByRole("button", { name: label("network.map_unfold").replace("%s", "calme-1") }).click();
      await page.waitForTimeout(300);
      await shoot(page, "carte-quarante-ouverte", theme);
    });
  });
}

test("carte complète à 400 px de large", async ({ page }) => {
  await serve(page, demoMap());
  await page.addInitScript(() => localStorage.setItem("theme", "light"));
  await page.setViewportSize({ width: 400, height: 900 });
  await page.goto("/");
  await expect(page.locator(".netmap")).toBeVisible();
  await page.waitForTimeout(300);
  await page.screenshot({ path: new URL("R1-carte-mobile-clair.png", folder).pathname, fullPage: true });
});
