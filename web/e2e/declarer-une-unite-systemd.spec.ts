import type { Page } from "@playwright/test";

import { label } from "./support/catalog";
import { expect, test } from "./support/findings";

// Les instances systemd (chantier L2). Les données fictives portent, sur la
// machine distante, six unités relevées (Forgejo déclaré sans port, une
// sauvegarde de nuit en échec, des unités que personne ne déclare), et sur la
// machine openCloud un Caddy déclaré que le relevé ne connaît pas. Les tests
// se suivent et partagent cet état ; le fichier passe avant le parcours
// générique (ordre alphabétique).
test.describe.configure({ mode: "serial" });

const options = (page: Page) => page.locator("[role=listbox] [role=option]");

async function openUnits(page: Page, machine: string): Promise<void> {
  await page.goto("/machines");
  await page.getByRole("link", { name: machine, exact: true }).first().click();
  await page.getByRole("link", { name: label("module.systemd_units"), exact: true }).click();
  await expect(page.getByText(label("systemd.intro"))).toBeVisible();
}

test("l'onglet des unités : l'état de chacune, le service déclaré, filtre et vue", async ({ page, findings }) => {
  await openUnits(page, "vps-paris-1");
  const rows = page.locator("main table tbody tr");
  await expect(rows).toHaveCount(6);
  await expect(page.locator("main")).toContainText(/6 sur 6 unités/);

  // Forgejo tourne et son service est un lien ; la sauvegarde de nuit est en échec, non déclarée.
  const forgejo = rows.filter({ hasText: "forgejo.service" });
  await expect(forgejo).toContainText(label("systemd.active_active"));
  await expect(forgejo.getByRole("link", { name: "forgejo", exact: true })).toBeVisible();
  const nightly = rows.filter({ hasText: "nightly-backup.service" });
  await expect(nightly).toContainText(label("systemd.active_failed"));
  await expect(nightly).toContainText(label("systemd.not_declared"));

  // Le filtre cherche dans le nom.
  await page.locator("#systemd-filter").fill("FORGE");
  await expect(rows).toHaveCount(1);
  await expect(page.locator("main")).toContainText(/1 sur 6 unités/);
  await page.locator("#systemd-filter").fill("zzz");
  await expect(page.getByText(label("systemd.none_match"))).toBeVisible();
  await page.locator("#systemd-filter").fill("");

  // Chaque vue du menu garde ce qu'elle dit.
  await page.locator("#systemd-view").click();
  await options(page).filter({ hasText: label("systemd.view_failed") }).click();
  await expect(rows).toHaveCount(1);
  await expect(rows.first()).toContainText("nightly-backup.service");
  await page.locator("#systemd-view").click();
  await options(page).filter({ hasText: label("systemd.view_declared") }).click();
  await expect(rows).toHaveCount(1);
  await expect(rows.first()).toContainText("forgejo.service");
  await page.locator("#systemd-view").click();
  await options(page).filter({ hasText: label("systemd.view_running") }).click();
  await expect(rows).toHaveCount(4);
  await page.locator("#systemd-view").click();
  await options(page).filter({ hasText: label("systemd.view_all") }).click();
  await expect(rows).toHaveCount(6);
  expect(findings.unknown()).toEqual([]);
});

test("systemd illisible : la page dit pourquoi, sans tableau", async ({ page, findings }) => {
  await page.route("**/api/machines/*/systemd-units", async (route) => {
    const response = await route.fetch();
    await route.fulfill({ response, json: { ...(await response.json()), available: false, reason: "not_booted", units: [] } });
  });
  await openUnits(page, "vps-paris-1");
  await expect(page.getByText(label("systemd.unavailable"), { exact: false })).toBeVisible();
  await expect(page.getByText(label("systemd.reason_not_booted"))).toBeVisible();
  await expect(page.locator("main table")).toHaveCount(0);
  expect(findings.unknown()).toEqual([]);
});

test("déclarer une unité relevée : l'assistant part du nom et de l'unité, le port se demande sans chercher", async ({ page, findings }) => {
  await openUnits(page, "vps-paris-1");
  await page.locator("main table tbody tr", { hasText: "ssh.service" }).getByRole("link", { name: label("systemd.declare") }).click();
  const dialog = page.locator("dialog[open]");
  await expect(dialog.getByRole("heading", { name: label("directory.accept_unit_title") })).toBeVisible();
  await expect(dialog.locator("#service-name")).toHaveValue("ssh");
  await dialog.getByRole("button", { name: label("wizard.next"), exact: true }).click();
  await expect(dialog.locator("#unit-0-systemd")).toHaveValue("ssh.service");
  // Le port est là, hors du pli « Avancé ».
  await expect(dialog.locator("#unit-0-ports")).toBeVisible();
  await dialog.locator("#unit-0-ports").fill("22");
  await dialog.getByRole("button", { name: label("wizard.next"), exact: true }).click();
  await dialog.getByRole("button", { name: label("wizard.next"), exact: true }).click();
  await expect(dialog.locator(".wizard-summary")).toBeVisible();
  await dialog.getByRole("button", { name: label("directory.create"), exact: true }).click();

  // La fiche dit l'état de l'unité, lu sur la machine, et le port déclaré.
  await expect(page).toHaveURL(/\/services\/annuaire\/svc_/);
  await expect(page.locator("main")).toContainText(label("systemd.active_active"));
  await expect(page.locator("main")).toContainText("22");
  await expect(page.locator("main")).not.toContainText(label("directory.port_to_declare"));
  expect(findings.unknown()).toEqual([]);
});

test("un binaire sans port : la fiche le dit et mène à l'étape qui le demande", async ({ page, findings }) => {
  await openUnits(page, "vps-paris-1");
  await page.locator("main table tbody tr", { hasText: "forgejo.service" }).getByRole("link", { name: "forgejo", exact: true }).click();
  await expect(page).toHaveURL(/\/services\/annuaire\/svc_/);
  await expect(page.locator("main")).toContainText(label("systemd.active_active"));
  await expect(page.locator("main")).toContainText(label("directory.port_to_declare"));

  await page.getByRole("link", { name: label("directory.port_to_declare"), exact: true }).click();
  const dialog = page.locator("dialog[open]");
  // L'édition s'ouvre droit sur l'étape des unités, port visible.
  await expect(dialog.locator("#unit-0-ports")).toBeVisible();
  await dialog.locator("#unit-0-ports").fill("3000");
  await dialog.getByRole("button", { name: label("wizard.back_to_summary"), exact: true }).click();
  await dialog.getByRole("button", { name: label("directory.save"), exact: true }).click();
  await expect(page.locator("main")).toContainText("3000");
  await expect(page.locator("main")).not.toContainText(label("directory.port_to_declare"));
  expect(findings.unknown()).toEqual([]);
});

test("une unité déclarée que la machine ne connaît pas se dit absente, avec quoi vérifier", async ({ page, findings }) => {
  await page.goto("/services");
  await page.locator("main").getByRole("link", { name: "caddy", exact: true }).first().click();
  await expect(page.locator("main")).toContainText(label("systemd.presence_missing"));
  await expect(page.locator("main [title]", { hasText: label("systemd.presence_missing") })).toHaveAttribute("title", label("systemd.presence_missing_help"));
  expect(findings.unknown()).toEqual([]);
});

test("« Déclarer un service » mène aux unités d'une machine pour un binaire", async ({ page, findings }) => {
  await page.goto("/services/annuaire/nouveau");
  const dialog = page.locator("dialog[open]");
  await expect(dialog.getByText(label("directory.pick_units"))).toBeVisible();
  await dialog.getByRole("link", { name: "vps-paris-1", exact: true }).click();
  await expect(page.getByText(label("systemd.intro"))).toBeVisible();
  await expect(page).toHaveURL(/\/machines\/[^/]+\/systemd$/);
  expect(findings.unknown()).toEqual([]);
});
