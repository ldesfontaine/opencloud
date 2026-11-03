import type { Locator, Page } from "@playwright/test";

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
  const note = page.locator(".note-neutral", { hasText: label("public_url.example_label") });
  await expect(note).toBeVisible();
  // La forme attendue est un exemple : aucune ligne à copier, donc un seul bouton Copier, celui de la relance.
  await expect(note.locator(".public-url-example")).toContainText('public_url = "https://opencloud.exemple.fr"');
  await expect(note.getByRole("button", { name: label("machine.copy") })).toHaveCount(1);
  await expect(note.locator(".token-box")).toHaveCount(1);
  await expect(note.locator(".token-box")).toContainText("sudo systemctl restart opencloud.service");
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

// L'édition de chaque assistant d'un écran, menée jusqu'à l'enregistrement :
// les Réglages repliés s'ouvrent, un menu déroulant y choisit une option, puis
// la fenêtre est rouverte pour relire ce qui a été gardé.
const normalized = (text: string) => text.replace(/\s+/g, " ").trim();

async function openSettings(dialog: Locator): Promise<void> {
  await dialog.locator("details.fold > summary").first().click();
}

// Ouvre le menu, choisit une autre option que la courante (ou la seule qu'il y a)
// et rend son premier mot, que le bouton du menu doit ensuite porter.
async function chooseOther(page: Page, dialog: Locator, trigger: string): Promise<string> {
  await dialog.locator(trigger).click();
  const list = page.locator("[role=listbox]").last();
  await expect(list).toBeVisible();
  const usable = list.locator("[role=option]:not([aria-disabled=true])");
  const other = usable.and(list.locator("[aria-selected=false]"));
  const option = (await other.count()) > 0 ? other.first() : usable.first();
  const chosen = normalized(await option.innerText()).split(" ")[0] ?? "";
  await option.click();
  await expect(list).toBeHidden();
  return chosen;
}

async function hrefOfLink(page: Page, list: string, name: string): Promise<string> {
  await page.goto(list);
  const href = await page.locator("main a", { hasText: name }).first().getAttribute("href");
  expect(href, `lien « ${name} » dans ${list}`).not.toBeNull();
  return href ?? "";
}

test("modifier une sonde : service choisi dans un menu, seuil changé, relus après enregistrement", async ({ page, findings }) => {
  const path = await hrefOfLink(page, "/disponibilite", "site essai u3 bis");
  findings.where = `${path}/modifier`;
  await page.goto(`${path}/modifier`);
  const dialog = page.locator("dialog[open]");
  await expectOneScreen(dialog, label("probe.save"));
  await openSettings(dialog);
  const service = await chooseOther(page, dialog, "#probe-service");
  await dialog.locator("#probe-failure").fill("4");
  await dialog.getByRole("button", { name: label("probe.save"), exact: true }).click();
  await expect(page).toHaveURL(/\/disponibilite\/[^/]+$/);

  await page.goto(`${path}/modifier`);
  const again = page.locator("dialog[open]");
  await openSettings(again);
  await expect(again.locator("#probe-failure")).toHaveValue("4");
  await expect(again.locator("#probe-service")).toContainText(service);
  expect(findings.unknown()).toEqual([]);
});

test("modifier une tâche : machine choisie dans un menu, grâce changée, relues après enregistrement", async ({ page, findings }) => {
  const path = await hrefOfLink(page, "/taches", "sauvegarde essai u3");
  findings.where = `${path}/modifier`;
  await page.goto(`${path}/modifier`);
  const dialog = page.locator("dialog[open]");
  await expectOneScreen(dialog, label("job.save"));
  await openSettings(dialog);
  const machine = await chooseOther(page, dialog, "#job-machine");
  await dialog.locator("#job-grace").fill("20");
  await dialog.getByRole("button", { name: label("job.save"), exact: true }).click();
  await expect(page).toHaveURL(/\/taches\/[^/]+$/);

  await page.goto(`${path}/modifier`);
  const again = page.locator("dialog[open]");
  await openSettings(again);
  await expect(again.locator("#job-grace")).toHaveValue("20");
  await expect(again.locator("#job-machine")).toContainText(machine);
  expect(findings.unknown()).toEqual([]);
});

test("modifier un canal : gravité choisie dans un menu, nom changé, relus après enregistrement", async ({ page, findings }) => {
  const path = await hrefOfLink(page, "/parametres", "salon essai u3");
  findings.where = path;
  await page.goto(path);
  const dialog = page.locator("dialog[open]");
  await expectOneScreen(dialog, label("channel.save"));
  await dialog.locator("#channel-name").fill("salon essai u3 bis");
  await openSettings(dialog);
  const severity = await chooseOther(page, dialog, "#channel-severity");
  await dialog.getByRole("button", { name: label("channel.save"), exact: true }).click();
  await expect(page).toHaveURL(/\/parametres$/);

  await page.goto(path);
  const again = page.locator("dialog[open]");
  await expect(again.locator("#channel-name")).toHaveValue("salon essai u3 bis");
  await openSettings(again);
  await expect(again.locator("#channel-severity")).toContainText(severity);
  expect(findings.unknown()).toEqual([]);
});

test("un silence : type choisi dans un menu et motif sous Réglages, relus dans la liste", async ({ page, findings }) => {
  findings.where = "/parametres/silences/nouveau";
  await page.goto("/parametres/silences/nouveau");
  const dialog = page.locator("dialog[open]");
  await openSettings(dialog);
  const kind = await chooseOther(page, dialog, "#silence-kind");
  await dialog.locator("#silence-reason").fill("relecture u3");
  await expect(dialog.locator(":scope details.fold > summary .fold-hint").first()).not.toContainText(label("silence.hint_kind_any"));
  await dialog.getByRole("button", { name: label("silence.create"), exact: true }).click();
  await expect(page).toHaveURL(/\/parametres$/);
  const row = page.locator("tr", { hasText: "relecture u3" });
  await expect(row).toBeVisible();
  await expect(row).toContainText(kind);
  expect(findings.unknown()).toEqual([]);
});

test("modifier un projet : titre changé, relu après enregistrement, page publique gardée", async ({ page, findings }) => {
  const path = await hrefOfLink(page, "/services/projets", "projet-essai-u3");
  findings.where = path;
  await page.goto(path);
  const dialog = page.locator("dialog[open]");
  await expectOneScreen(dialog, label("project.save"));
  await dialog.locator("#project-title").fill("Titre relu u3");
  await dialog.getByRole("button", { name: label("project.save"), exact: true }).click();
  await expect(page).toHaveURL(/\/services\/projets$/);

  await page.goto(path);
  const again = page.locator("dialog[open]");
  await expect(again.locator("#project-title")).toHaveValue("Titre relu u3");
  await expect(again.locator("#project-slug")).toBeVisible();
  expect(findings.unknown()).toEqual([]);
});

test("modifier un plan : destination choisie dans un menu, nombre gardé changé, relus après enregistrement", async ({ page, findings }) => {
  findings.where = "/sauvegardes/plans/database/modifier";
  await page.goto("/sauvegardes/plans/database/modifier");
  const dialog = page.locator("dialog[open]");
  await expectOneScreen(dialog, label("backup.plan_save"));
  const destination = await chooseOther(page, dialog, "#backup-plan-destination");
  await openSettings(dialog);
  await dialog.locator("#backup-plan-name").fill("base essai u3");
  await dialog.locator("#backup-plan-keep").fill("7");
  await dialog.getByRole("button", { name: label("backup.plan_save"), exact: true }).click();
  await expect(page).toHaveURL(/\/sauvegardes$/);

  await page.goto("/sauvegardes/plans/database/modifier");
  const again = page.locator("dialog[open]");
  await expect(again.locator("#backup-plan-destination")).toContainText(destination);
  await openSettings(again);
  await expect(again.locator("#backup-plan-keep")).toHaveValue("7");
  await expect(again.locator("#backup-plan-name")).toHaveValue("base essai u3");
  expect(findings.unknown()).toEqual([]);
});
