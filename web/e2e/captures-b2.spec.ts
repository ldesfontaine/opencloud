import { expect, test, type Locator, type Page } from "@playwright/test";

import { label } from "./support/catalog";

// Les captures de B2 (docs/projet/refonte-ux/captures/B2-*.png) : la page avec
// ses services entiers, une carte « à remplacer » (nuit-pi), l'assistant d'un
// service avec ce qui part, l'accès à une base et un volume, et l'enrôlement
// avec le droit restic.docker. Données fictives seulement. Hors `make ci` :
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
  await page.screenshot({ path: new URL(`B2-${name}-${theme === "light" ? "clair" : "sombre"}.png`, folder).pathname, fullPage });
}

function card(page: Page, name: string): Locator {
  return page.locator("article.backup-source").filter({ has: page.locator("h3", { hasText: new RegExp(`^${name}$`) }) });
}

for (const theme of ["light", "dark"] as const) {
  test(`page Sauvegardes ${theme}`, async ({ page }) => {
    await open(page, theme, "/sauvegardes");
    await expect(page.locator(".backup-verdict")).toBeVisible();
    await shoot(page, "page", theme, true);
  });

  test(`service entier et menu de sa carte ${theme}`, async ({ page }) => {
    await open(page, theme, "/sauvegardes");
    const recettes = card(page, "recettes");
    await recettes.scrollIntoViewIfNeeded();
    await recettes.getByRole("button", { name: label("entity.more").replace("%s", "recettes") }).click();
    await expect(page.getByRole("menu")).toBeVisible();
    await shoot(page, "service-menu", theme);
  });

  test(`nuit-pi à remplacer ${theme}`, async ({ page }) => {
    await open(page, theme, "/sauvegardes");
    const mail = card(page, "mail");
    await mail.scrollIntoViewIfNeeded();
    await shoot(page, "a-remplacer", theme);
  });

  test(`assistant d'un service entier ${theme}`, async ({ page }) => {
    await open(page, theme, "/sauvegardes");
    const response = await page.request.get("/api/backups/overview");
    const overview = (await response.json()) as { sources: { key: string; name: string }[] };
    const key = overview.sources.find((source) => source.name === "photos")?.key ?? "";
    await page.goto(`/sauvegardes/nouvelle?source=${encodeURIComponent(key)}`);
    await page.locator("dialog[open] .backup-parts").scrollIntoViewIfNeeded();
    await expect(page.locator("dialog[open] .backup-parts")).toBeVisible();
    await shoot(page, "assistant-service", theme);
  });

  test(`accéder à une base et un volume ${theme}`, async ({ page }) => {
    await open(page, theme, "/sauvegardes");
    const access = page.locator("section[aria-labelledby=backup-access-title]");
    await access.getByRole("combobox", { name: label("backup.access_pick") }).click();
    await page.getByRole("option", { name: /^recettes/ }).click();
    await access.scrollIntoViewIfNeeded();
    await access.getByRole("combobox", { name: label("backup.access_pick") }).click();
    await expect(page.getByRole("listbox")).toBeVisible();
    await shoot(page, "acceder-menu", theme);
  });

  test(`enrôlement avec restic.docker ${theme}`, async ({ page }) => {
    await open(page, theme, "/machines/nouvelle");
    const dialog = page.locator("dialog[open]");
    await dialog.getByLabel(label("machine.name_label")).fill("vps-essai-2");
    await dialog.getByText(label("machine.rights_advanced")).click();
    await dialog.getByLabel(label("machine.capability_restic_docker")).check();
    await shoot(page, "droit-restic-docker", theme);
  });
}
