import { expect, test, type Page } from "@playwright/test";

import { label } from "./support/catalog";
import { bigMap, flatMap, fullMap } from "./support/netmap";
import type { NetworkMap } from "../src/api/network";

// Les captures de R1 (docs/projet/refonte-ux/captures/R1-*.png) : la carte
// Réseau de la vue d'ensemble. La carte réelle vient du serveur de données
// fictives ; les trois autres (tout configuré, tout à plat, quarante machines)
// sont des réponses de la même forme servies par le parcours, car les données
// fictives du serveur ne montrent pas ces cas (le serveur, lui, les prouve en
// Go : internal/netmap). Hors `make ci` : `E2E_CAPTURES=1 npm run e2e`.
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
  // Les traits se posent après la mesure des boîtes.
  await expect(page.locator(".netmap-links .netmap-edge")).not.toHaveCount(0);
  await page.waitForTimeout(300);
}

for (const theme of ["light", "dark"] as const) {
  test.describe(theme, () => {
    test.beforeEach(async ({ page }) => {
      await page.addInitScript((value) => localStorage.setItem("theme", value), theme);
      await page.setViewportSize({ width: 1440, height: 1000 });
      await page.clock.setFixedTime(new Date("2026-09-12T12:00:00Z"));
    });

    test(`carte réelle du serveur ${theme}`, async ({ page }) => {
      await page.goto("/");
      await expect(page.locator(".netmap")).toBeVisible();
      await page.waitForTimeout(400);
      await shoot(page, "carte-reelle", theme);
    });

    test(`carte complète, tunnel, pare-feu et zones ${theme}`, async ({ page }) => {
      await serve(page, fullMap());
      await openOverview(page);
      await shoot(page, "carte-complete", theme);
    });

    test(`carte complète, survol d'un lien et liste des liens ouverte ${theme}`, async ({ page }) => {
      await serve(page, fullMap());
      await openOverview(page);
      await page.locator(".netmap-edge.tunnel").focus();
      await expect(page.locator(".netmap-tip")).toBeVisible();
      await shoot(page, "carte-survol-lien", theme);
      await page.mouse.move(0, 0);
      await page.locator(".netmap-wrap summary", { hasText: label("network.map_links_title").replace("%s", "5") }).click();
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
  await serve(page, fullMap());
  await page.addInitScript(() => localStorage.setItem("theme", "light"));
  await page.setViewportSize({ width: 400, height: 900 });
  await openOverview(page);
  await page.screenshot({ path: new URL("R1-carte-mobile-clair.png", folder).pathname, fullPage: true });
});
