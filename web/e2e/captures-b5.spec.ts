import { expect, test, type Locator, type Page } from "@playwright/test";

import { label } from "./support/catalog";

// Les captures de B5 (docs/projet/refonte-ux/captures/B5-*.png) : la page
// Sauvegardes (cartes, la base d'openCloud en ligne de plus), la matrice avec
// son inspecteur, l'explorateur de fichiers ouvert sur un instantané, son refus
// de droit, le menu « … » d'une carte, la tuile de la vue d'ensemble et
// l'enrôlement avec le droit restic.browse. Données fictives seulement. Hors
// `make ci` : `E2E_CAPTURES=1 npm run e2e`.
test.skip(process.env.E2E_CAPTURES !== "1", "captures demandées par E2E_CAPTURES=1");

const folder = new URL("../../docs/projet/refonte-ux/captures/", import.meta.url);

async function open(page: Page, theme: "light" | "dark", path: string): Promise<void> {
  await page.setViewportSize({ width: 1500, height: 900 });
  await page.clock.setFixedTime(new Date("2026-09-12T12:00:00Z"));
  await page.addInitScript((value) => localStorage.setItem("theme", value), theme);
  await page.goto(path);
}

async function shoot(page: Page, name: string, theme: "light" | "dark", fullPage = false): Promise<void> {
  await page.waitForTimeout(250);
  await page.screenshot({ path: new URL(`B5-${name}-${theme === "light" ? "clair" : "sombre"}.png`, folder).pathname, fullPage });
}

function card(page: Page, name: string): Locator {
  return page.locator("article.backup-source").filter({ has: page.locator("h3", { hasText: new RegExp(`^${name}$`) }) });
}

async function openMatrix(page: Page): Promise<void> {
  await page.getByRole("radiogroup", { name: label("backup.view_label") }).getByRole("radio", { name: label("backup.view_matrix") }).check();
  await expect(page.locator(".matrix table")).toBeVisible();
}

for (const theme of ["light", "dark"] as const) {
  test(`page Sauvegardes, la base en ligne de plus ${theme}`, async ({ page }) => {
    await open(page, theme, "/sauvegardes");
    await expect(page.locator(".backup-verdict")).toBeVisible();
    await shoot(page, "page", theme, true);
  });

  test(`matrice et inspecteur ${theme}`, async ({ page }) => {
    await open(page, theme, "/sauvegardes");
    await openMatrix(page);
    await page.locator(".matrix").getByRole("button", { name: /^paperless, pi : / }).click();
    await expect(page.locator("aside.inspector")).toBeVisible();
    // La section entière : la matrice, large, et son inspecteur.
    await page.waitForTimeout(250);
    await page.locator("section[aria-labelledby=backup-sources-title]").screenshot({ path: new URL(`B5-matrice-${theme === "light" ? "clair" : "sombre"}.png`, folder).pathname });
  });

  test(`menu d'une carte avec « Parcourir » ${theme}`, async ({ page }) => {
    await open(page, theme, "/sauvegardes");
    const paperless = card(page, "paperless");
    await paperless.scrollIntoViewIfNeeded();
    await paperless.getByRole("button", { name: label("entity.more").replace("%s", "paperless") }).click();
    await expect(page.getByRole("menu")).toBeVisible();
    await shoot(page, "menu", theme);
  });

  test(`explorateur ouvert sur un instantané ${theme}`, async ({ page }) => {
    await open(page, theme, "/sauvegardes");
    const paperless = card(page, "paperless");
    await paperless.getByRole("button", { name: label("entity.more").replace("%s", "paperless") }).click();
    await page.getByRole("menuitem", { name: label("backup.browse") }).click();
    test.setTimeout(90_000);
    const dialog = page.locator("dialog[open]");
    await dialog.getByRole("button", { name: "srv", exact: true }).click({ timeout: 30_000 });
    await dialog.getByRole("button", { name: "appli", exact: true }).click({ timeout: 30_000 });
    await dialog.getByRole("checkbox", { name: label("explorer.choose").replace("%s", "app.ini") }).check({ timeout: 30_000 });
    await expect(dialog.locator(".explorer-outputs")).toContainText(label("backup.restore_beside").split("%s")[0] ?? "");
    await shoot(page, "explorateur", theme);
    // La confirmation en ligne dit le chemin exact ; confirmée, le résultat se lit dans la fenêtre.
    await dialog.getByRole("button", { name: label("backup.restore"), exact: true }).click();
    await expect(dialog.getByRole("group", { name: /\.restaure-\d{8}-\d{6}/ })).toBeVisible();
    await shoot(page, "restaurer-confirmation", theme);
    await dialog.getByRole("button", { name: label("confirm.yes") }).click();
    await expect(dialog.locator(".backup-restore-results li").first()).toContainText(label("action.state_succeeded"), { timeout: 30_000 });
    await shoot(page, "restaurer-resultat", theme);
  });

  test(`explorateur sur une cible sans le droit ${theme}`, async ({ page }) => {
    await open(page, theme, "/sauvegardes");
    const calendrier = card(page, "calendrier");
    await calendrier.getByRole("button", { name: label("entity.more").replace("%s", "calendrier") }).click();
    await page.getByRole("menuitem", { name: label("backup.browse") }).click();
    await expect(page.locator("dialog[open]")).toContainText(label("restic.target_browse_right_missing"));
    await shoot(page, "explorateur-sans-droit", theme);
  });

  test(`tuile de la vue d'ensemble ${theme}`, async ({ page }) => {
    await open(page, theme, "/");
    await expect(page.locator("a.stat-link", { hasText: label("module.backups") })).toBeVisible();
    await shoot(page, "vue-ensemble", theme);
  });

  test(`restaurer sans le droit ${theme}`, async ({ page }) => {
    await open(page, theme, "/sauvegardes");
    test.setTimeout(90_000);
    const vaultwarden = card(page, "vaultwarden");
    await vaultwarden.getByRole("button", { name: label("entity.more").replace("%s", "vaultwarden") }).click();
    await page.getByRole("menuitem", { name: label("backup.browse") }).click();
    const dialog = page.locator("dialog[open]");
    await dialog.getByRole("button", { name: "srv", exact: true }).click({ timeout: 30_000 });
    await dialog.getByRole("button", { name: "appli", exact: true }).click({ timeout: 30_000 });
    await dialog.getByRole("checkbox", { name: label("explorer.choose").replace("%s", "app.ini") }).check({ timeout: 30_000 });
    await dialog.getByRole("button", { name: label("backup.restore"), exact: true }).click();
    await dialog.getByRole("button", { name: label("confirm.yes") }).click();
    await expect(dialog).toContainText(label("restic.source_restore_right_missing"), { timeout: 30_000 });
    await shoot(page, "restaurer-sans-droit", theme);
  });

  test(`enrôlement avec le droit restic.browse ${theme}`, async ({ page }) => {
    await open(page, theme, "/machines/nouvelle");
    const dialog = page.locator("dialog[open]");
    await dialog.getByText(label("machine.rights_advanced")).click();
    await dialog.getByRole("checkbox", { name: label("machine.capability_restic_browse") }).check();
    await shoot(page, "droit-restic-browse", theme);
  });
}
