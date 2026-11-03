import { expect, test, type Page } from "@playwright/test";

import { label } from "./support/catalog";

// Les captures de B6 (docs/projet/refonte-ux/captures/B6-*.png) : la fenêtre
// « Modifier » une sauvegarde restic, rétention baissée (le nom de la
// sauvegarde est exigé), puis la même fenêtre menu ouvert, et le journal avec
// la ligne de la baisse. Données fictives seulement. Hors `make ci` :
// `E2E_CAPTURES=1 npm run e2e`.
test.skip(process.env.E2E_CAPTURES !== "1", "captures demandées par E2E_CAPTURES=1");

const folder = new URL("../../docs/projet/refonte-ux/captures/", import.meta.url);

async function open(page: Page, theme: "light" | "dark", path: string): Promise<void> {
  await page.setViewportSize({ width: 1500, height: 900 });
  await page.clock.setFixedTime(new Date("2026-09-12T12:00:00Z"));
  await page.addInitScript((value) => localStorage.setItem("theme", value), theme);
  await page.goto(path);
}

async function shoot(page: Page, name: string, theme: "light" | "dark"): Promise<void> {
  await page.waitForTimeout(250);
  await page.screenshot({ path: new URL(`B6-${name}-${theme === "light" ? "clair" : "sombre"}.png`, folder).pathname });
}

async function openEdit(page: Page): Promise<void> {
  const card = page.locator("article.backup-source").filter({ has: page.locator("h3", { hasText: /^paperless$/ }) });
  await card.scrollIntoViewIfNeeded();
  await card.getByRole("button", { name: label("entity.more").replace("%s", "paperless") }).click();
  await expect(page.getByRole("menu")).toBeVisible();
  await page.getByRole("menuitem", { name: label("backup.restic_edit") }).click();
  await expect(page.locator("dialog[open] #restic-name")).toBeVisible();
}

for (const theme of ["light", "dark"] as const) {
  test(`baisser une rétention, le nom exigé ${theme}`, async ({ page }) => {
    await open(page, theme, "/sauvegardes");
    await openEdit(page);
    const dialog = page.locator("dialog[open]");
    await dialog.locator("#restic-keep_daily").fill("7");
    await expect(dialog.locator("#restic-confirm-name")).toBeVisible();
    await dialog.locator("#restic-confirm-name").fill("pap");
    await shoot(page, "baisse-retention", theme);
  });

  test(`le menu de la carte, la fenêtre de baisse ouverte ensuite ${theme}`, async ({ page }) => {
    await open(page, theme, "/sauvegardes");
    const card = page.locator("article.backup-source").filter({ has: page.locator("h3", { hasText: /^paperless$/ }) });
    await card.scrollIntoViewIfNeeded();
    await card.getByRole("button", { name: label("entity.more").replace("%s", "paperless") }).click();
    await expect(page.getByRole("menu")).toBeVisible();
    await shoot(page, "menu-carte", theme);
  });

  test(`la baisse au journal ${theme}`, async ({ page }) => {
    await open(page, theme, "/sauvegardes");
    await openEdit(page);
    const dialog = page.locator("dialog[open]");
    // Les deux thèmes partagent le serveur : chacun baisse d'une nuit de plus.
    const daily = dialog.locator("#restic-keep_daily");
    await daily.fill(String(Number(await daily.inputValue()) - 1));
    await dialog.locator("#restic-confirm-name").fill("paperless");
    await dialog.getByRole("button", { name: label("backup.plan_save") }).click();
    await expect(dialog).toHaveCount(0);
    await page.goto("/journal");
    await expect(page.locator("tbody tr", { hasText: label("audit.verb_retention_lower") }).first()).toBeVisible();
    await shoot(page, "journal", theme);
  });
}
