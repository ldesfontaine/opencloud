import type { Locator, Page } from "@playwright/test";

import { label } from "./support/catalog";
import { expect, test } from "./support/findings";

// B3 : la configuration d'un pare-feu OPNsense se sauvegarde. Le serveur la lit
// avec la clé du module Pare-feu (rien ne part vers un agent) ; Bordure répond
// à l'export, Secours n'a pas le privilège « Diagnostics: Configuration
// History ». Chaque bouton est cliqué, chaque menu ouvert et une option
// choisie, l'assistant mené au bout. Les tests se suivent : ils partagent le
// serveur.

function card(page: Page, name: string): Locator {
  return page.locator("article.backup-source").filter({ has: page.locator("h3", { hasText: new RegExp(`^${name}$`) }) });
}

async function openBackups(page: Page): Promise<void> {
  await page.clock.setFixedTime(new Date("2026-09-12T12:00:00Z"));
  await page.goto("/sauvegardes");
  await expect(page.locator(".backup-verdict")).toBeVisible();
}

test.describe.configure({ mode: "serial" });

test("un pare-feu déclaré est une source : sa carte dit ce qu'il manque et mène à l'assistant, déjà sur lui", async ({ page, findings }) => {
  findings.where = "/sauvegardes › pare-feu";
  await openBackups(page);
  const bordure = card(page, "Bordure");
  await expect(bordure).toContainText(label("backup.status_none"));
  await expect(bordure).toContainText(label("backup.missing_firewall_plan"));
  await bordure.getByRole("link", { name: label("backup.save") }).click();
  await expect(page).toHaveURL(/\/sauvegardes\/plans\/nouveau\?firewall=/);
  const dialog = page.locator("dialog[open]");
  // Le pare-feu est choisi, le nom du plan proposé, le privilège dit en clair.
  await expect(dialog.getByRole("combobox", { name: label("backup.plan_firewall") })).toContainText("Bordure");
  await expect(dialog.getByLabel(label("backup.plan_subject"))).toHaveValue("bordure");
  await expect(dialog).toContainText("Diagnostics: Configuration History");
  await expect(dialog.getByRole("link", { name: label("backup.plan_firewall_open") })).toBeVisible();
  expect(findings.unknown()).toEqual([]);
});

test("sans le privilège d'export, l'assistant refuse et dit le libellé exact", async ({ page, findings }) => {
  findings.where = "/sauvegardes/plans/nouveau (Secours, sans privilège)";
  await openBackups(page);
  await page.goto("/sauvegardes/plans/nouveau");
  const dialog = page.locator("dialog[open]");
  // Le menu de la source s'ouvre, l'option « configuration d'un pare-feu » se choisit.
  await dialog.getByRole("combobox", { name: label("backup.plan_source") }).click();
  await page.getByRole("option", { name: label("backup.source_kind_firewall") }).click();
  await dialog.getByRole("combobox", { name: label("backup.plan_firewall") }).click();
  await page.getByRole("option", { name: "Secours" }).click();
  await dialog.getByLabel(label("backup.plan_subject")).fill("secours");
  await dialog.getByRole("button", { name: label("backup.plan_add") }).click();
  await expect(dialog.locator(".note-danger")).toContainText("Diagnostics: Configuration History");
  await expect(dialog).toBeVisible();
  // Rien n'a été créé : la page ne montre pas de plan « secours ».
  const plans = (await (await page.request.get("/api/backups")).json()) as { plans: { subject: string }[] };
  expect(plans.plans.map((plan) => plan.subject)).not.toContain("secours");
  await dialog.getByRole("button", { name: label("modal.close") }).first().click();
  expect(findings.unknown()).toEqual([]);
});

test("l'assistant mené au bout : Bordure part chiffrée chaque nuit, la carte le montre et le plan se lance", async ({ page, findings }) => {
  findings.where = "/sauvegardes/plans/nouveau (Bordure)";
  await openBackups(page);
  await page.goto("/sauvegardes/plans/nouveau");
  const dialog = page.locator("dialog[open]");
  await dialog.getByRole("combobox", { name: label("backup.plan_source") }).click();
  await page.getByRole("option", { name: label("backup.source_kind_firewall") }).click();
  // Sans pare-feu choisi, l'assistant le dit et reste ouvert.
  await dialog.getByLabel(label("backup.plan_subject")).fill("bordure");
  await dialog.getByRole("button", { name: label("backup.plan_add") }).click();
  await expect(dialog.locator(".note-danger")).toContainText(label("backup.plan_firewall_missing"));
  await dialog.getByRole("combobox", { name: label("backup.plan_firewall") }).click();
  await page.getByRole("option", { name: "Bordure" }).click();
  await dialog.getByRole("button", { name: label("backup.plan_add") }).click();
  await expect(dialog).toHaveCount(0);

  const bordure = card(page, "Bordure");
  await expect(bordure).toContainText(label("backup.origin_archive").split("{product}")[1] ?? "");
  await expect(bordure).toContainText(label("backup.status_never"));

  // Le plan se gère depuis le menu de la carte : lancer, modifier, supprimer.
  await bordure.getByRole("button", { name: label("entity.more").replace("%s", "Bordure") }).click();
  await expect(page.getByRole("menu").getByRole("menuitem")).toContainText([label("backup.run_now"), label("backup.plan_edit"), label("backup.plan_delete")]);
  await page.keyboard.press("Escape");
  expect(findings.unknown()).toEqual([]);
});
