import { expect, test, type Page } from "@playwright/test";

import { label } from "./support/catalog";

// Les captures de B3 (docs/projet/refonte-ux/captures/B3-*.png) : la carte d'un
// pare-feu à sauvegarder, l'assistant sur lui, son menu de pare-feu ouvert, et
// le refus qui nomme le privilège « Diagnostics: Configuration History ».
// Données fictives seulement. Hors `make ci` : `E2E_CAPTURES=1 npm run e2e`.
test.skip(process.env.E2E_CAPTURES !== "1", "captures demandées par E2E_CAPTURES=1");

const folder = new URL("../../docs/projet/refonte-ux/captures/", import.meta.url);

async function open(page: Page, theme: "light" | "dark", path: string): Promise<void> {
  await page.clock.setFixedTime(new Date("2026-09-12T12:00:00Z"));
  await page.addInitScript((value) => localStorage.setItem("theme", value), theme);
  await page.goto(path);
}

async function shoot(page: Page, name: string, theme: "light" | "dark", fullPage = false): Promise<void> {
  await page.waitForTimeout(200);
  await page.screenshot({ path: new URL(`B3-${name}-${theme === "light" ? "clair" : "sombre"}.png`, folder).pathname, fullPage });
}

async function firewallID(page: Page, name: string): Promise<string> {
  const answer = (await (await page.request.get("/api/firewalls")).json()) as { firewalls: { id: string; name: string }[] };
  return answer.firewalls.find((firewall) => firewall.name === name)?.id ?? "";
}

for (const theme of ["light", "dark"] as const) {
  test(`carte du pare-feu à sauvegarder ${theme}`, async ({ page }) => {
    await open(page, theme, "/sauvegardes");
    const card = page.locator("article.backup-source").filter({ has: page.locator("h3", { hasText: /^Bordure$/ }) });
    await card.scrollIntoViewIfNeeded();
    await expect(card).toContainText(label("backup.missing_firewall_plan"));
    await shoot(page, "carte", theme);
  });

  test(`assistant sur le pare-feu ${theme}`, async ({ page }) => {
    await open(page, theme, `/sauvegardes/plans/nouveau?firewall=${await firewallID(page, "Bordure")}`);
    const dialog = page.locator("dialog[open]");
    await expect(dialog).toContainText("Diagnostics: Configuration History");
    await shoot(page, "assistant", theme);
  });

  test(`menu des pare-feu ouvert ${theme}`, async ({ page }) => {
    await open(page, theme, `/sauvegardes/plans/nouveau?firewall=${await firewallID(page, "Bordure")}`);
    const dialog = page.locator("dialog[open]");
    await dialog.getByRole("combobox", { name: label("backup.plan_firewall") }).click();
    await expect(page.getByRole("listbox")).toBeVisible();
    await shoot(page, "menu", theme);
  });

  test(`refus sans le privilège ${theme}`, async ({ page }) => {
    await open(page, theme, `/sauvegardes/plans/nouveau?firewall=${await firewallID(page, "Secours")}`);
    const dialog = page.locator("dialog[open]");
    await dialog.getByRole("button", { name: label("backup.plan_add") }).click();
    await expect(dialog.locator(".note-danger")).toContainText("Diagnostics: Configuration History");
    await shoot(page, "refus", theme);
  });
}
