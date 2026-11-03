import type { Page } from "@playwright/test";

import { label } from "./support/catalog";
import { createFirstAccount, mockFirstSetup } from "./support/firstSetup";
import { expect, test } from "@playwright/test";

// Les captures de U1 (docs/projet/refonte-ux/captures/U1-*.png) : l'installation
// d'une machine en étapes, le ré-enrôlement avec le droit coché, un sélecteur
// qui grise les machines qui ne conviennent pas, la première configuration et
// la fiche d'un agent qui n'a pas le droit de se mettre à jour. Données
// fictives seulement (la première configuration et l'offre de mise à jour sont
// simulées côté navigateur). Hors `make ci` : `E2E_CAPTURES=1 npm run e2e`.
test.skip(process.env.E2E_CAPTURES !== "1", "captures demandées par E2E_CAPTURES=1");

const folder = new URL("../../docs/projet/refonte-ux/captures/", import.meta.url);

async function shoot(page: Page, name: string, theme: "light" | "dark"): Promise<void> {
  await page.screenshot({ path: new URL(`U1-${name}-${theme === "light" ? "clair" : "sombre"}.png`, folder).pathname });
}

// L'horloge des données fictives est figée dans le passé : le jeton y paraît
// expiré. La capture montre celui d'une heure, comme sur un serveur vivant.
async function freshToken(page: Page): Promise<void> {
  await page.route("**/api/machines/**", async (route) => {
    if (route.request().method() !== "POST") {
      await route.continue();
      return;
    }
    const response = await route.fetch();
    if (!response.ok() || response.status() === 204) {
      await route.fulfill({ response });
      return;
    }
    const body = (await response.json()) as { expires_at?: string };
    if (body.expires_at !== undefined) {
      body.expires_at = new Date(Date.now() + 3_599_000).toISOString();
    }
    await route.fulfill({ response, json: body });
  });
}

async function openInstallSteps(page: Page): Promise<void> {
  await freshToken(page);
  await page.goto("/machines/nouvelle");
  const dialog = page.locator("dialog[open]");
  await dialog.getByLabel(label("machine.name_label")).fill("vps-lyon-2");
  await dialog.getByText(label("machine.rights_advanced")).click();
  await dialog.getByLabel(label("machine.capability_docker_deploy")).check();
  await dialog.getByLabel(label("machine.capability_backup_receive")).check();
  await dialog.locator("#machine-backup-path").fill("/srv/sauvegardes");
  await dialog.getByRole("button", { name: label("machine.create_token") }).click();
  await expect(dialog.locator(".checklist-item").first()).toBeVisible();
}

for (const theme of ["light", "dark"] as const) {
  test.describe(theme, () => {
    test.beforeEach(async ({ page }) => {
      await page.addInitScript((value) => localStorage.setItem("theme", value), theme);
      await page.setViewportSize({ width: 1280, height: 1100 });
    });

    test(`installation ${theme}`, async ({ page }) => {
      await openInstallSteps(page);
      await shoot(page, "installation-etapes", theme);
    });

    test(`reenrolement avec le droit ${theme}`, async ({ page }) => {
      await freshToken(page);
      await page.goto("/sauvegardes/destinations/nouvelle");
      await page.locator("dialog[open] #backup-destination-machine").click();
      await shoot(page, "destination-menu-machines", theme);
      const note = page.locator("[role=listbox] .select-option-group").first();
      await note.getByRole("link", { name: label("machine.reenroll_with_right") }).click();
      const dialog = page.locator("dialog[open]");
      await expect(dialog.getByRole("checkbox", { name: label("machine.capability_backup_receive") })).toBeChecked();
      await dialog.locator("#machine-backup-path").fill("/srv/sauvegardes");
      await shoot(page, "reenroler-droit-coche", theme);
      await dialog.getByRole("button", { name: label("machine.rights_submit") }).click();
      await expect(dialog.locator(".checklist-item").first()).toContainText(label("machine.install_stop_title"));
      await shoot(page, "reenroler-etapes", theme);
    });

    test(`plan de sauvegarde ${theme}`, async ({ page }) => {
      await page.goto("/sauvegardes/plans/nouveau");
      const dialog = page.locator("dialog[open]");
      await dialog.locator("#backup-plan-kind").click();
      await page.locator("[role=listbox] [role=option]").filter({ hasText: label("backup.source_kind_agent") }).click();
      await dialog.locator("#backup-plan-machine").click();
      await expect(page.locator("[role=listbox]")).toBeVisible();
      await shoot(page, "plan-menu-machines", theme);
    });

    test(`premiere configuration ${theme}`, async ({ page }) => {
      await mockFirstSetup(page);
      await page.goto("/");
      await expect(page.locator("#auth-username")).toBeVisible();
      await createFirstAccount(page);
      await expect(page.locator(".checklist-item").first()).toBeVisible();
      await shoot(page, "premiere-configuration-agent", theme);
    });

    test(`agent sans le droit ${theme}`, async ({ page }) => {
      await page.route("**/api/machines/*/agent-update", (route) =>
        route.fulfill({ json: { current: "v0.0.1", target: "v0.0.2", arch: "amd64", available: false, reason: "right_missing" } }),
      );
      await page.goto("/machines");
      await page.locator("table a.svc").first().click();
      await page.goto(`${new URL(page.url()).pathname.replace(/\/$/, "")}/agent`);
      await expect(page.getByRole("link", { name: label("machine.reenroll_with").replace("%s", label("machine.capability_agent_update")) })).toBeVisible();
      await shoot(page, "agent-droit-manquant", theme);
    });
  });
}
