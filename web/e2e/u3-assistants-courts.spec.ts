import type { Locator } from "@playwright/test";

import { label } from "./support/catalog";
import { expect, test } from "./support/findings";

// Le chantier U3 (#450) : ce qui tient en un écran n'est plus un assistant.
// Chaque fenêtre s'ouvre sur ses champs et l'action finale, sans barre
// d'étapes ni récapitulatif ; les réglages sont repliés sous « Réglages », avec
// leurs valeurs par défaut dites en clair. Les tests écrivent pour de bon sur
// les données fictives : le fichier passe après le parcours générique (ordre
// alphabétique), qui n'en voit rien.
test.describe.configure({ mode: "serial" });

async function expectOneScreen(dialog: Locator, create: string): Promise<void> {
  await expect(dialog.locator(".wizard-bar")).toHaveCount(0);
  await expect(dialog.locator(".wizard-summary")).toHaveCount(0);
  await expect(dialog.getByRole("button", { name: label("wizard.next"), exact: true })).toHaveCount(0);
  await expect(dialog.getByRole("button", { name: create, exact: true })).toBeVisible();
}

test("une sonde : un nom, une adresse, Créer ; les réglages repliés disent leurs valeurs", async ({ page, findings }) => {
  findings.where = "/disponibilite/nouvelle";
  await page.goto("/disponibilite/nouvelle");
  const dialog = page.locator("dialog[open]");
  await expectOneScreen(dialog, label("probe.create"));

  // Deux champs à l'écran ; le rythme n'y est pas.
  await expect(dialog.locator("#probe-name")).toBeVisible();
  await expect(dialog.locator("#probe-target")).toBeVisible();
  await expect(dialog.locator("#probe-interval")).toBeHidden();
  const settings = dialog.locator("details.fold").first();
  await expect(settings.locator(":scope > summary")).toContainText(label("wizard.settings"));
  await expect(settings.locator(":scope > summary .fold-hint")).toContainText(/HTTP depuis .+, chaque minute, 3 échecs avant l'alerte/);

  await dialog.locator("#probe-name").fill("site essai u3");
  await dialog.locator("#probe-target").fill("https://exemple.fr/");

  // Un réglage faux dans le pli fermé : le navigateur ne peut pas le montrer,
  // le pli s'ouvre dessus et rien n'est envoyé.
  await settings.locator(":scope > summary").click();
  await dialog.locator("#probe-interval").fill("10");
  await settings.locator(":scope > summary").click();
  await expect(settings).not.toHaveAttribute("open", "");
  await dialog.getByRole("button", { name: label("probe.create"), exact: true }).click();
  await expect(settings).toHaveAttribute("open", "");
  await expect(dialog.locator("#probe-interval")).toBeVisible();
  expect(new URL(page.url()).pathname).toBe("/disponibilite/nouvelle");

  // Corrigé, le rythme se relit tel qu'il s'affichera partout : « toutes les 2 min ».
  await dialog.locator("#probe-interval").fill("120");
  await expect(settings.locator(":scope > summary .fold-hint")).toContainText(/toutes les 2 min/);
  await dialog.getByRole("button", { name: label("probe.create"), exact: true }).click();
  await expect(page).toHaveURL(/\/disponibilite\/[^/]+$/);
  await expect(page.getByRole("heading", { name: "site essai u3" })).toBeVisible();
  await expect(page.locator("main").getByText(/toutes les 2 min/)).toBeVisible();

  // La même fenêtre modifie : elle s'ouvre sur les champs, pas sur un récapitulatif.
  await page.goto(`${new URL(page.url()).pathname}/modifier`);
  const edit = page.locator("dialog[open]");
  await expectOneScreen(edit, label("probe.save"));
  await expect(edit.locator("#probe-name")).toHaveValue("site essai u3");
  await edit.locator("#probe-name").fill("site essai u3 bis");
  await edit.getByRole("button", { name: label("probe.save"), exact: true }).click();
  await expect(page.getByRole("heading", { name: "site essai u3 bis" })).toBeVisible();
  expect(findings.unknown()).toEqual([]);
});

test("une tâche : un nom, un intervalle, Créer ; la note de l'adresse locale est neutre et porte la ligne à poser", async ({ page, findings }) => {
  findings.where = "/taches/nouvelle";
  await page.goto("/taches/nouvelle");
  const dialog = page.locator("dialog[open]");
  await expectOneScreen(dialog, label("job.create"));
  await expect(dialog.locator("#job-grace")).toBeHidden();
  await expect(dialog.locator("details.fold .fold-hint")).toContainText(/10 min de grâce, aucune machine/);
  await dialog.locator("#job-name").fill("sauvegarde essai u3");
  await dialog.getByRole("button", { name: label("job.create"), exact: true }).click();

  // L'adresse du banc est locale : une note neutre, pas une alerte rouge.
  await expect(page).toHaveURL(/\/taches\/[^/]+$/);
  const note = page.locator(".note-neutral", { hasText: label("public_url.local_note").replace("{config_path}", "/etc/opencloud/config.toml") });
  await expect(note).toBeVisible();
  await expect(note.locator(".token-box").first()).toContainText('public_url = "https://…"');
  await expect(note.locator(".token-box").nth(1)).toContainText(label("public_url.restart_command").replace("{server_unit}", "opencloud.service"));
  await expect(page.locator(".note-danger")).toHaveCount(0);
  expect(findings.unknown()).toEqual([]);
});

test("un canal : où partent les alertes en un écran, lesquelles sous Réglages", async ({ page, findings }) => {
  findings.where = "/parametres/canaux/nouveau";
  await page.goto("/parametres/canaux/nouveau");
  const dialog = page.locator("dialog[open]");
  await expectOneScreen(dialog, label("channel.create"));
  await expect(dialog.locator("#channel-severity")).toBeHidden();
  const settings = dialog.locator("details.fold").first();
  await expect(settings.locator(":scope > summary .fold-hint")).toContainText(/dès .+, avec les résolutions/);
  await dialog.locator("#channel-name").fill("salon essai u3");
  await dialog.locator("#channel-url").fill("https://hooks.exemple.fr/oc");
  await settings.locator(":scope > summary").click();
  await dialog.getByLabel(label("channel.enabled_label")).uncheck();
  await expect(settings.locator(":scope > summary .fold-hint")).toContainText(label("channel.hint_disabled"));
  await dialog.getByRole("button", { name: label("channel.create"), exact: true }).click();
  await expect(page).toHaveURL(/\/parametres$/);
  await expect(page.getByText("salon essai u3").first()).toBeVisible();
  expect(findings.unknown()).toEqual([]);
});

test("un silence : l'objet et la durée en un écran, le type et le début sous Réglages", async ({ page, findings }) => {
  findings.where = "/parametres/silences/nouveau";
  await page.goto("/parametres/silences/nouveau");
  const dialog = page.locator("dialog[open]");
  await expectOneScreen(dialog, label("silence.create"));
  await expect(dialog.locator("#silence-kind")).toBeHidden();
  await expect(dialog.locator("details.fold .fold-hint")).toContainText(/tous les types, dès maintenant/);
  // Viser un objet sans le choisir : le refus se lit dans la fenêtre.
  await dialog.locator("#silence-target-kind").click();
  await page.locator("[role=listbox] [role=option]", { hasText: label("silence.target_kind_machine") }).click();
  await dialog.getByRole("button", { name: label("silence.create"), exact: true }).click();
  await expect(dialog.locator("[role=alert]")).toBeVisible();
  await dialog.locator("#silence-target-kind").click();
  await page.locator("[role=listbox] [role=option]", { hasText: label("silence.target_kind_none") }).click();
  await dialog.getByRole("button", { name: label("silence.create"), exact: true }).click();
  await expect(page).toHaveURL(/\/parametres$/);
  expect(findings.unknown()).toEqual([]);
});

test("un projet : le nom et la page publique en un écran", async ({ page, findings }) => {
  findings.where = "/services/projets/nouveau";
  await page.goto("/services/projets/nouveau");
  const dialog = page.locator("dialog[open]");
  await expectOneScreen(dialog, label("project.create"));
  await dialog.locator("#project-name").fill("projet-essai-u3");
  await dialog.getByLabel(label("project.publish_check")).check();
  await expect(dialog.locator("#project-slug")).toBeVisible();
  await dialog.getByRole("button", { name: label("project.create"), exact: true }).click();
  await expect(page).toHaveURL(/\/services\/projets$/);
  await expect(page.getByText("projet-essai-u3").first()).toBeVisible();
  expect(findings.unknown()).toEqual([]);
});

test("un plan de sauvegarde : la source et la destination en un écran, le nombre gardé sous Réglages", async ({ page, findings }) => {
  findings.where = "/sauvegardes/plans/nouveau";
  await page.goto("/sauvegardes/plans/nouveau");
  const dialog = page.locator("dialog[open]");
  await expectOneScreen(dialog, label("backup.plan_add"));
  await expect(dialog.locator("#backup-plan-keep")).toBeHidden();
  await expect(dialog.locator("details.fold .fold-hint")).toContainText(/14 sauvegardes gardées, chaque nuit/);
  await dialog.locator("details.fold summary").click();
  await expect(dialog.locator("#backup-plan-keep")).toBeVisible();
  await dialog.getByRole("button", { name: label("modal.cancel"), exact: true }).first().click();
  expect(findings.unknown()).toEqual([]);
});
