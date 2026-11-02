import { expect, test, type Page } from "@playwright/test";

import { label } from "./support/catalog";

// Les captures de B1 (docs/projet/refonte-ux/captures/B1-*.png) : la page
// Sauvegardes, l'assistant « Sauvegarder… » à chaque étape, la fenêtre d'une
// cible avec son menu ouvert, le menu d'une carte, celui d'« Accéder », la
// rétention à modifier, le rappel de la phrase de passe, et le binaire
// manquant (la cible coffre sans rest-server, la source atelier sans restic). Données fictives
// seulement, horloge figée sur celle du serveur. Hors `make ci` :
// `E2E_CAPTURES=1 npm run e2e`.
test.skip(process.env.E2E_CAPTURES !== "1", "captures demandées par E2E_CAPTURES=1");

const folder = new URL("../../docs/projet/refonte-ux/captures/", import.meta.url);

function filled(key: string, ...args: string[]): string {
  return args.reduce<string>((text, value) => text.replace(/%[sd]/, value), label(key));
}

async function open(page: Page, theme: "light" | "dark", path: string): Promise<void> {
  await page.clock.setFixedTime(new Date("2026-09-12T12:00:00Z"));
  await page.addInitScript((value) => localStorage.setItem("theme", value), theme);
  await page.goto(path);
  await expect(page.locator(".backup-verdict")).toBeVisible();
}

async function shoot(page: Page, name: string, theme: "light" | "dark", fullPage = false): Promise<void> {
  await page.waitForTimeout(200);
  await page.screenshot({ path: new URL(`B1-${name}-${theme === "light" ? "clair" : "sombre"}.png`, folder).pathname, fullPage });
}

async function next(page: Page): Promise<void> {
  await page.locator("dialog[open]").getByRole("button", { name: label("wizard.next") }).click();
}

for (const theme of ["light", "dark"] as const) {
  test(`page Sauvegardes ${theme}`, async ({ page }) => {
    await open(page, theme, "/sauvegardes");
    await shoot(page, "page", theme, true);
  });

  test(`page Sauvegardes sur téléphone ${theme}`, async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await open(page, theme, "/sauvegardes");
    await shoot(page, "mobile", theme);
  });

  test(`menu d'une carte restic ${theme}`, async ({ page }) => {
    await open(page, theme, "/sauvegardes");
    const card = page.locator("article.backup-source", { has: page.locator("h3", { hasText: /^vaultwarden$/ }) });
    await card.scrollIntoViewIfNeeded();
    await card.getByRole("button", { name: filled("entity.more", "vaultwarden") }).click();
    await expect(page.getByRole("menu")).toBeVisible();
    await shoot(page, "carte-menu", theme);
  });

  test(`accéder, menu ouvert ${theme}`, async ({ page }) => {
    await open(page, theme, "/sauvegardes");
    const access = page.locator("section[aria-labelledby=backup-access-title]");
    await access.scrollIntoViewIfNeeded();
    await access.getByRole("combobox", { name: label("backup.access_pick") }).click();
    await expect(page.locator("[role=listbox]")).toBeVisible();
    await shoot(page, "acceder-menu", theme);
  });

  test(`assistant, les trois étapes ${theme}`, async ({ page }) => {
    await open(page, theme, "/sauvegardes/nouvelle");
    await expect(page.locator("dialog[open] .pick-list")).toBeVisible();
    await shoot(page, "assistant-quoi", theme);
    await next(page);
    await shoot(page, "assistant-ou", theme);
    await next(page);
    await expect(page.locator("dialog[open] .wizard-summary")).toBeVisible();
    await shoot(page, "assistant-recap", theme);
  });

  test(`binaire manquant ${theme}`, async ({ page }) => {
    await open(page, theme, "/sauvegardes");
    const coffre = page.locator("article.backup-target", { has: page.locator("h3", { hasText: /^coffre$/ }) });
    await coffre.scrollIntoViewIfNeeded();
    await shoot(page, "binaire-cible", theme);
    const grimoire = page.locator("article.backup-source", { has: page.locator("h3", { hasText: /^grimoire$/ }) });
    await grimoire.scrollIntoViewIfNeeded();
    await shoot(page, "binaire-source", theme);
  });

  test(`assistant, la rétention et le plancher ${theme}`, async ({ page }) => {
    await open(page, theme, "/sauvegardes/nouvelle");
    await next(page);
    const dialog = page.locator("dialog[open]");
    await dialog.locator("summary", { hasText: label("wizard.settings") }).click();
    await dialog.locator("#restic-keep_daily").scrollIntoViewIfNeeded();
    await shoot(page, "assistant-retention", theme);
  });

  test(`faire d'une machine une cible, menu ouvert ${theme}`, async ({ page }) => {
    await open(page, theme, "/sauvegardes/cibles/nouvelle");
    const dialog = page.locator("dialog[open]");
    await expect(dialog.locator("#restic-target-host")).toBeVisible();
    await shoot(page, "cible", theme);
    await dialog.locator("#restic-target-machine").click();
    await expect(page.locator("[role=listbox]")).toBeVisible();
    await shoot(page, "cible-menu", theme);
  });

  test(`modifier une sauvegarde ${theme}`, async ({ page }) => {
    await open(page, theme, "/sauvegardes");
    const card = page.locator("article.backup-source", { has: page.locator("h3", { hasText: /^paperless$/ }) });
    await card.scrollIntoViewIfNeeded();
    await card.getByRole("button", { name: filled("entity.more", "paperless") }).click();
    await page.getByRole("menuitem", { name: label("backup.restic_edit") }).click();
    await expect(page.locator("dialog[open] #restic-name")).toBeVisible();
    await shoot(page, "modifier", theme);
  });

  test(`la phrase de passe à noter ${theme}`, async ({ page }) => {
    await page.clock.setFixedTime(new Date("2026-09-12T12:00:00Z"));
    await page.addInitScript((value) => localStorage.setItem("theme", value), theme);
    await page.goto("/sauvegardes/cle");
    const dialog = page.locator("dialog[open]");
    await dialog.locator("#backup-passphrase").fill("une phrase de passe assez longue");
    await dialog.locator("#backup-passphrase-confirm").fill("une phrase de passe assez longue");
    await dialog.getByRole("button", { name: label("backup.keyring_replace") }).click();
    await page.locator("dialog[open]").last().getByRole("button", { name: label("backup.keyring_replace") }).last().click();
    await expect(page.locator("dialog[open]")).toContainText(label("backup.passphrase_note_now"));
    await shoot(page, "cle-notee", theme);
  });
}
