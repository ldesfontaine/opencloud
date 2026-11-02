import type { Page } from "@playwright/test";

import { label } from "./support/catalog";
import { expect, test } from "@playwright/test";

// Les captures de L5 (docs/projet/refonte-ux/captures/L5-*.png) : le droit
// « Lire l'environnement des conteneurs » dans la fenêtre « Ajouter une
// machine » (coché, avec sa note) et menu du rôle ouvert, la fiche du service
// « notes » (lien lu dans l'environnement, note du droit manquant et son
// bouton), et la fenêtre « Ré-enrôler » ouverte par ce bouton, droit déjà
// coché. Rien ne s'écrit : on s'arrête avant « Créer le jeton ». Données
// fictives seulement. Hors `make ci` : `E2E_CAPTURES=1 npm run e2e`.
test.skip(process.env.E2E_CAPTURES !== "1", "captures demandées par E2E_CAPTURES=1");

const folder = new URL("../../docs/projet/refonte-ux/captures/", import.meta.url);

async function shoot(page: Page, name: string, theme: "light" | "dark"): Promise<void> {
  await page.screenshot({ path: new URL(`L5-${name}-${theme === "light" ? "clair" : "sombre"}.png`, folder).pathname });
}

async function openService(page: Page, name: string): Promise<void> {
  await page.goto("/services");
  await page.locator("main").getByRole("link", { name: new RegExp(`^${name}(\\s|$)`) }).first().click();
  await expect(page.getByRole("heading", { name, exact: true })).toBeVisible();
  await expect(page.locator("main section", { has: page.getByText(label("link.title"), { exact: true }) }).locator("tbody tr").first()).toBeVisible();
}

for (const theme of ["light", "dark"] as const) {
  test.describe(theme, () => {
    test.beforeEach(async ({ page }) => {
      await page.addInitScript((value) => localStorage.setItem("theme", value), theme);
      await page.setViewportSize({ width: 1280, height: 900 });
      await page.clock.setFixedTime(new Date("2026-09-12T12:00:00Z"));
    });

    test(`Ajouter une machine, droit d'environnement coché ${theme}`, async ({ page }) => {
      await page.goto("/machines/nouvelle");
      const dialog = page.locator("dialog[open]");
      await dialog.getByLabel(label("machine.name_label")).fill("vps-env-1");
      await dialog.getByText(label("machine.rights_advanced")).click();
      await dialog.getByRole("checkbox", { name: label("machine.capability_observe_env") }).check();
      await expect(dialog.getByText(label("machine.capability_observe_env_note"))).toBeVisible();
      await dialog.getByText(label("machine.capability_observe_env_note")).scrollIntoViewIfNeeded();
      await shoot(page, "ajouter-droit", theme);
    });

    test(`Ajouter une machine, menu du rôle ouvert ${theme}`, async ({ page }) => {
      await page.goto("/machines/nouvelle");
      const dialog = page.locator("dialog[open]");
      await dialog.getByLabel(label("machine.name_label")).fill("vps-env-1");
      await dialog.locator("#machine-role").click();
      await expect(page.locator("[role=listbox] [role=option]").first()).toBeVisible();
      await shoot(page, "ajouter-menu-role", theme);
    });

    test(`fiche d'un service, lien lu et droit manquant ${theme}`, async ({ page }) => {
      await openService(page, "notes");
      await expect(page.locator(".note", { hasText: label("link.environment_off") })).toBeVisible();
      await page.getByText(label("link.title"), { exact: true }).scrollIntoViewIfNeeded();
      await shoot(page, "fiche-liens", theme);
    });

    test(`Ré-enrôler ouvert par le bouton du droit manquant ${theme}`, async ({ page }) => {
      await openService(page, "notes");
      await page.locator(".note", { hasText: label("link.environment_off") }).getByRole("link").click();
      const dialog = page.locator("dialog[open]");
      await expect(dialog.getByRole("checkbox", { name: label("machine.capability_observe_env") })).toBeChecked();
      await dialog.getByText(label("machine.capability_observe_env_note")).scrollIntoViewIfNeeded();
      await shoot(page, "reenroller-coche", theme);
    });
  });
}
