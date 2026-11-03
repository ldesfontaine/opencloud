import { expect, test, type Locator, type Page } from "@playwright/test";

import { label } from "./support/catalog";

// Les captures de B4 (docs/projet/refonte-ux/captures/B4-*.png) : la page
// Sauvegardes avec sa surveillance et le test de restauration de chaque carte,
// le menu « Vérifier maintenant », une cible qui n'a pas le droit de tester,
// la fiche d'une alerte de sauvegarde, la boîte des alertes et l'enrôlement avec
// le droit restic.verify. Données fictives seulement. Hors `make ci` :
// `E2E_CAPTURES=1 npm run e2e`.
test.skip(process.env.E2E_CAPTURES !== "1", "captures demandées par E2E_CAPTURES=1");

const folder = new URL("../../docs/projet/refonte-ux/captures/", import.meta.url);

async function open(page: Page, theme: "light" | "dark", path: string): Promise<void> {
  await page.clock.setFixedTime(new Date("2026-09-12T12:00:00Z"));
  await page.addInitScript((value) => localStorage.setItem("theme", value), theme);
  await page.goto(path);
}

async function shoot(page: Page, name: string, theme: "light" | "dark", fullPage = false): Promise<void> {
  await page.waitForTimeout(200);
  await page.screenshot({ path: new URL(`B4-${name}-${theme === "light" ? "clair" : "sombre"}.png`, folder).pathname, fullPage });
}

function card(page: Page, name: string): Locator {
  return page.locator("article.backup-source").filter({ has: page.locator("h3", { hasText: new RegExp(`^${name}$`) }) });
}

for (const theme of ["light", "dark"] as const) {
  test(`page Sauvegardes, surveillance en tête ${theme}`, async ({ page }) => {
    await open(page, theme, "/sauvegardes");
    await expect(page.locator(".backup-verdict")).toBeVisible();
    await expect(page.locator(".alert-row").first()).toBeVisible();
    await shoot(page, "page", theme, true);
  });

  test(`test de restauration d'une carte et son menu ${theme}`, async ({ page }) => {
    await open(page, theme, "/sauvegardes");
    const intranet = card(page, "intranet");
    await intranet.scrollIntoViewIfNeeded();
    await intranet.getByRole("button", { name: label("entity.more").replace("%s", "intranet") }).click();
    await expect(page.getByRole("menu")).toBeVisible();
    await shoot(page, "menu-verifier", theme);
  });

  test(`cible sans le droit de tester ${theme}`, async ({ page }) => {
    await open(page, theme, "/sauvegardes");
    const calendrier = card(page, "calendrier");
    await calendrier.scrollIntoViewIfNeeded();
    await calendrier.getByRole("button", { name: label("entity.more").replace("%s", "calendrier") }).click();
    await page.getByRole("menu").getByRole("menuitem", { name: label("backup.verify_now") }).click();
    await expect(calendrier).toContainText(label("restic.target_verify_right_missing"));
    await calendrier.scrollIntoViewIfNeeded();
    await shoot(page, "cible-sans-droit", theme);
  });

  test(`fiche d'une alerte de sauvegarde ${theme}`, async ({ page }) => {
    await open(page, theme, "/sauvegardes");
    await page.locator(".alert-row", { hasText: label("alert.fact_restore_test_stale").split("%")[0]?.trim() ?? "" }).first().getByRole("link").first().click();
    await expect(page).toHaveURL(/\/alertes\/alerte\/\d+/);
    await shoot(page, "alerte", theme);
  });

  test(`boîte des alertes filtrée sur les sources sans plan ${theme}`, async ({ page }) => {
    await open(page, theme, "/alertes?genre=source_unprotected");
    await expect(page.locator(".alert-row").first()).toBeVisible();
    await shoot(page, "alertes", theme, true);
  });

  test(`enrôlement avec restic.verify ${theme}`, async ({ page }) => {
    await open(page, theme, "/machines/nouvelle");
    const dialog = page.locator("dialog[open]");
    await dialog.getByLabel(label("machine.name_label")).fill("nas-sauvegardes");
    await dialog.getByText(label("machine.rights_advanced")).click();
    await dialog.getByLabel(label("machine.capability_restic_verify")).check();
    await shoot(page, "droit-restic-verify", theme);
  });
}
