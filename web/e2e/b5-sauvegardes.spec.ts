import type { Locator, Page } from "@playwright/test";

import { label } from "./support/catalog";
import { expect, test } from "./support/findings";

// La page Sauvegardes de B5, contre les données fictives : la matrice, son
// inspecteur et l'explorateur de fichiers. pi (restic.target + restic.browse)
// répond aux parcours avec un même arbre : trois instantanés, /srv/appli et
// ses fichiers. stockage, cible de calendrier, n'a pas restic.browse. Chaque
// bouton est cliqué, chaque menu ouvert avec un choix, chaque fenêtre menée
// au bout. Les tests se suivent : ils partagent le serveur.

function filled(key: string, ...args: (string | number)[]): string {
  return args.reduce<string>((text, value) => text.replace(/%[sd]/, String(value)), label(key));
}

function card(page: Page, name: string): Locator {
  return page.locator("article.backup-source").filter({ has: page.locator("h3", { hasText: new RegExp(`^${name}$`) }) });
}

async function openBackups(page: Page): Promise<void> {
  await page.clock.setFixedTime(new Date("2026-09-12T12:00:00Z"));
  await page.goto("/sauvegardes");
  await expect(page.locator(".backup-verdict")).toBeVisible();
}

async function openMatrix(page: Page): Promise<void> {
  await openBackups(page);
  await page.getByRole("radiogroup", { name: label("backup.view_label") }).getByRole("radio", { name: label("backup.view_matrix") }).check();
  await expect(page.locator(".matrix table")).toBeVisible();
}

test.describe.configure({ mode: "serial" });

// Un parcours attend la réponse d'une cible, dont les signaux ont un débit borné
// (une réponse toutes les deux secondes une fois le seau vidé par l'amorçage).
const browseTimeout = 30_000;

test("la matrice : une ligne par source, une colonne par endroit, la légende, « Nulle part »", async ({ page, findings }) => {
  findings.where = "/sauvegardes › matrice";
  await openMatrix(page);
  const matrix = page.locator(".matrix");
  await expect(matrix.getByRole("columnheader", { name: new RegExp(`^pi ${label("backup.matrix_place_restic")}`) })).toBeVisible();
  await expect(matrix.getByRole("columnheader", { name: new RegExp(label("backup.matrix_nowhere")) })).toBeVisible();
  await expect(matrix.getByRole("rowheader", { name: /paperless/ })).toBeVisible();
  // Une source sans copie a sa case « Nulle part » ; une copie a son état et ses copies gardées.
  await expect(matrix.getByRole("button", { name: new RegExp(`, ${label("backup.matrix_nowhere")} : ${label("backup.matrix_none")}$`) }).first()).toBeVisible();
  await expect(matrix.getByRole("button", { name: new RegExp(`^paperless, pi : `) })).toBeVisible();
  for (const key of ["ok", "warn", "danger", "observed", "none"]) {
    await expect(matrix.locator(".matrix-legend")).toContainText(label(`backup.matrix_legend_${key}`));
  }
  // Les cartes reviennent, avec leur filtre.
  await page.getByRole("radiogroup", { name: label("backup.view_label") }).getByRole("radio", { name: label("backup.view_cards") }).check();
  await expect(page.locator(".backup-grid")).toBeVisible();
  await expect(page.locator(".matrix")).toHaveCount(0);
  expect(findings.unknown()).toEqual([]);
});

test("l'inspecteur : une case l'ouvre, ses gestes, un menu choisi, il se ferme", async ({ page, findings }) => {
  findings.where = "/sauvegardes › inspecteur";
  await openMatrix(page);
  await page.locator(".matrix").getByRole("button", { name: new RegExp(`^paperless, pi : `) }).click();
  const inspector = page.getByRole("complementary", { name: filled("inspector.label", "paperless") });
  await expect(inspector).toBeVisible();
  await expect(inspector).toContainText(label("backup.fact_retention"));
  await expect(inspector).toContainText(label("backup.inspector_nights"));
  await expect(inspector).toContainText(filled("backup.inspector_back_restic", "pi"));
  for (const gesture of ["backup.browse", "backup.run_now", "backup.verify_now", "backup.restic_edit", "backup.restic_delete"]) {
    await expect(inspector.getByText(label(gesture), { exact: true })).toBeVisible();
  }
  // La suppression demande confirmation avant d'agir : on la ferme sans rien perdre.
  await inspector.getByRole("button", { name: label("backup.restic_delete") }).click();
  const confirm = page.locator("dialog[open]");
  await expect(confirm).toContainText(filled("backup.restic_delete_confirm", "paperless", "pi"));
  await confirm.getByRole("button", { name: label("modal.cancel") }).click();
  await expect(confirm).toHaveCount(0);
  // « Modifier » mène à la fenêtre de la sauvegarde.
  await inspector.getByRole("link", { name: label("backup.restic_edit") }).click();
  await expect(page).toHaveURL(/\/sauvegardes\/restic\/[^/]+\/[^/]+\/modifier/);
  await page.locator("dialog[open]").getByRole("button", { name: label("modal.close") }).first().click();
  await expect(page).toHaveURL(/\/sauvegardes$/);
  // Une source sans copie : le verdict, et ce qui manque ou le geste qui commence.
  await openMatrix(page);
  const nowhere = page.locator(".matrix").getByRole("button", { name: new RegExp(`, ${label("backup.matrix_nowhere")} : `) }).first();
  const name = ((await nowhere.getAttribute("aria-label")) ?? "").split(",")[0] ?? "";
  await nowhere.click();
  const start = page.getByRole("complementary", { name: filled("inspector.label", name) });
  await expect(start).toContainText(label("backup.status_none"));
  await start.getByRole("button", { name: label("inspector.close") }).click();
  await expect(start).toHaveCount(0);
  expect(findings.unknown()).toEqual([]);
});

test("l'explorateur : les instantanés, un dossier ouvert, une sélection, le clavier, Échap", async ({ page, findings }) => {
  const slow = expect.configure({ timeout: browseTimeout });
  findings.where = "/sauvegardes/parcourir";
  await openMatrix(page);
  await page.locator(".matrix").getByRole("button", { name: new RegExp(`^paperless, pi : `) }).click();
  await page.getByRole("complementary", { name: filled("inspector.label", "paperless") }).getByRole("link", { name: label("backup.browse") }).click();
  test.setTimeout(90_000);
  const dialog = page.locator("dialog[open]");
  await slow(page).toHaveURL(/\/sauvegardes\/parcourir\//);
  await slow(dialog).toContainText(filled("backup.explorer_title", "paperless"));
  await slow(dialog).toContainText(label("backup.explorer_readonly"));
  // Rien à restaurer tant que rien n'est choisi à la racine.
  await slow(dialog.locator(".explorer-outputs")).toContainText(label("backup.restore_pick"));
  // Trois instantanés, le plus récent choisi ; la racine montre « srv ».
  const snapshots = dialog.getByRole("option", { name: /cccccccc/ });
  await slow(snapshots).toHaveAttribute("aria-selected", "true", { timeout: browseTimeout });
  await slow(dialog.getByRole("option", { name: /dddddddd/ })).toBeVisible();
  await slow(dialog.getByRole("option", { name: /eeeeeeee/ })).toBeVisible();
  await dialog.getByRole("button", { name: "srv", exact: true }).click({ timeout: browseTimeout });
  await dialog.getByRole("button", { name: "appli", exact: true }).click({ timeout: browseTimeout });
  await slow(dialog.getByRole("option", { name: /app\.ini/ })).toBeVisible({ timeout: browseTimeout });
  // Deux cases : un seul à la fois. Une seule : le bouton « Restaurer ».
  await dialog.getByRole("checkbox", { name: filled("explorer.choose", "app.ini") }).check();
  await dialog.getByRole("checkbox", { name: filled("explorer.choose", "docker-compose.yml") }).check();
  await slow(dialog.locator(".explorer-outputs")).toContainText(label("backup.restore_one_at_a_time"));
  await dialog.getByRole("checkbox", { name: filled("explorer.choose", "docker-compose.yml") }).uncheck();
  await slow(dialog.locator(".explorer-outputs")).toContainText(filled("explorer.chosen_one", 1));
  await dialog.getByRole("button", { name: label("backup.restore"), exact: true }).click();
  // La confirmation dit le chemin exact, à côté de l'original ; « Non » la ferme sans rien envoyer.
  const destination = /\/srv\/appli\/app\.ini\.restaure-\d{8}-\d{6}/;
  await slow(dialog.getByRole("group", { name: destination })).toBeVisible();
  await dialog.getByRole("button", { name: label("confirm.cancel") }).click();
  await slow(dialog.getByRole("button", { name: label("backup.restore"), exact: true })).toBeVisible();
  await expect(dialog.locator(".backup-restore-results")).toHaveCount(0);
  // Confirmé : l'ordre part à la machine source, le résultat se lit dans la fenêtre.
  await dialog.getByRole("button", { name: label("backup.restore"), exact: true }).click();
  await dialog.getByRole("button", { name: label("confirm.yes") }).click();
  await slow(dialog.locator(".backup-restore-results li").first()).toContainText(label("action.state_succeeded"));
  await slow(dialog.locator(".backup-restore-results li").first()).toContainText(destination);
  // Une copie déjà là : refus net, avec sa raison, rien d'écrasé.
  await dialog.getByRole("checkbox", { name: filled("explorer.choose", "app.ini") }).uncheck();
  await dialog.getByRole("checkbox", { name: filled("explorer.choose", "existe") }).check();
  await dialog.getByRole("button", { name: label("backup.restore"), exact: true }).click();
  await dialog.getByRole("button", { name: label("confirm.yes") }).click();
  await slow(dialog.locator(".backup-restore-results li").first()).toContainText(label("action.state_refused"));
  await slow(dialog.locator(".backup-restore-results li").first()).toContainText(label("action.reason_path_exists"));
  // Un autre instantané ramène à la racine.
  await dialog.getByRole("checkbox", { name: filled("explorer.choose", "existe") }).uncheck();
  await dialog.getByRole("option", { name: /dddddddd/ }).click();
  await slow(dialog.getByRole("button", { name: "srv", exact: true })).toBeVisible();
  // Le clavier : ↓ Entrée ouvre, Retour arrière remonte.
  const list = dialog.getByRole("listbox", { name: label("explorer.list") });
  await list.focus();
  await page.keyboard.press("Enter");
  await slow(dialog.getByRole("button", { name: "appli", exact: true })).toBeVisible();
  await page.keyboard.press("Backspace");
  await slow(dialog.getByRole("button", { name: "srv", exact: true })).toBeVisible();
  // Le fil d'Ariane et « Dossier parent ».
  await dialog.getByRole("button", { name: "srv", exact: true }).click();
  await dialog.getByRole("button", { name: label("explorer.up") }).click();
  await slow(dialog.getByRole("button", { name: "srv", exact: true })).toBeVisible();
  await page.keyboard.press("Escape");
  await slow(page).toHaveURL(/\/sauvegardes$/);
  expect(findings.unknown()).toEqual([]);
});

test("l'explorateur sur une cible sans restic.browse : le refus porte son geste, le menu de la carte y mène", async ({ page, findings }) => {
  findings.where = "/sauvegardes/parcourir › cible sans restic.browse";
  await openBackups(page);
  const calendrier = card(page, "calendrier");
  await calendrier.getByRole("button", { name: filled("entity.more", "calendrier") }).click();
  await page.getByRole("menu").getByRole("menuitem", { name: label("backup.browse") }).click();
  const dialog = page.locator("dialog[open]");
  await expect(dialog).toContainText(label("restic.target_browse_right_missing"), { timeout: browseTimeout });
  const gesture = dialog.getByRole("link", { name: filled("machine.reenroll_with", label("machine.capability_restic_browse")) });
  await expect(gesture).toBeVisible();
  await gesture.click();
  await expect(page).toHaveURL(/\/machines\/[^/]+\/reenroller\?droit=restic\.browse/);
  await expect(page.locator("dialog[open]").getByRole("checkbox", { name: label("machine.capability_restic_browse") })).toBeChecked();
  await page.locator("dialog[open]").getByRole("button", { name: label("modal.close") }).first().click();
  expect(findings.unknown()).toEqual([]);
});

test("restaurer sans le droit : le refus nomme le droit et porte « Ré-enrôler avec ce droit »", async ({ page, findings }) => {
  findings.where = "/sauvegardes/parcourir › restaurer sans restic.restore";
  test.setTimeout(90_000);
  const slow = expect.configure({ timeout: browseTimeout });
  await openBackups(page);
  // vaultwarden part de pve-nord, qui n'a pas restic.restore.
  const vaultwarden = card(page, "vaultwarden");
  await vaultwarden.getByRole("button", { name: label("entity.more").replace("%s", "vaultwarden") }).click();
  await page.getByRole("menuitem", { name: label("backup.browse") }).click();
  const dialog = page.locator("dialog[open]");
  await dialog.getByRole("button", { name: "srv", exact: true }).click({ timeout: browseTimeout });
  await dialog.getByRole("button", { name: "appli", exact: true }).click({ timeout: browseTimeout });
  await dialog.getByRole("checkbox", { name: filled("explorer.choose", "app.ini") }).check({ timeout: browseTimeout });
  await dialog.getByRole("button", { name: label("backup.restore"), exact: true }).click();
  await dialog.getByRole("button", { name: label("confirm.yes") }).click();
  await slow(dialog).toContainText(label("restic.source_restore_right_missing"));
  const gesture = dialog.getByRole("link", { name: filled("machine.reenroll_with", label("machine.capability_restic_restore")) });
  await slow(gesture).toBeVisible();
  await gesture.click();
  await slow(page).toHaveURL(/\/machines\/[^/]+\/reenroller\?droit=restic\.restore/);
  await expect(page.locator("dialog[open]").getByRole("checkbox", { name: label("machine.capability_restic_restore") })).toBeChecked();
  await page.locator("dialog[open]").getByRole("button", { name: label("modal.close") }).first().click();
  expect(findings.unknown()).toEqual([]);
});

test("la base d'openCloud est une ligne de plus : sa clé et ses destinations sont sur sa carte", async ({ page, findings }) => {
  findings.where = "/sauvegardes › base d'openCloud";
  await openBackups(page);
  const database = card(page, label("backup.source_database").replace("{product}", "openCloud"));
  await expect(database).toContainText(label("backup.keyring_title"));
  await database.getByRole("link", { name: new RegExp(`${label("backup.keyring_create")}|${label("backup.keyring_replace")}`) }).click();
  await expect(page).toHaveURL(/\/sauvegardes\/cle$/);
  await page.locator("dialog[open]").getByRole("button", { name: label("modal.close") }).first().click();
  await database.getByRole("link", { name: label("backup.destination_add") }).click();
  await expect(page).toHaveURL(/\/sauvegardes\/destinations\/nouvelle$/);
  await page.locator("dialog[open]").getByRole("button", { name: label("modal.close") }).first().click();
  await expect(page).toHaveURL(/\/sauvegardes$/);
  expect(findings.unknown()).toEqual([]);
});

test("la vue d'ensemble : la tuile Sauvegardes mène à la page", async ({ page, findings }) => {
  findings.where = "/ › tuile Sauvegardes";
  await page.goto("/");
  const tile = page.locator("a.stat-link", { hasText: label("module.backups") });
  await expect(tile).toBeVisible();
  await tile.click();
  await expect(page).toHaveURL(/\/sauvegardes$/);
  expect(findings.unknown()).toEqual([]);
});

test("l'enrôlement offre le droit « Parcourir les dépôts reçus », décoché d'avance, avec sa note", async ({ page, findings }) => {
  findings.where = "/machines/nouvelle › restic.browse";
  await page.goto("/machines/nouvelle");
  const dialog = page.locator("dialog[open]");
  await dialog.getByText(label("machine.rights_advanced")).click();
  const right = dialog.getByRole("checkbox", { name: label("machine.capability_restic_browse") });
  await expect(right).not.toBeChecked();
  await right.check();
  const note = label("machine.capability_restic_browse_note").split("{product}")[0]?.slice(0, 40) ?? "";
  await expect(dialog).toContainText(note);
  await right.uncheck();
  await expect(dialog.getByText(note)).toHaveCount(0);
  expect(findings.unknown()).toEqual([]);
});

test("l'enrôlement offre le droit « Restaurer des sauvegardes sur cette machine », décoché d'avance, avec sa note", async ({ page, findings }) => {
  findings.where = "/machines/nouvelle › restic.restore";
  await page.goto("/machines/nouvelle");
  const dialog = page.locator("dialog[open]");
  await dialog.getByText(label("machine.rights_advanced")).click();
  const right = dialog.getByRole("checkbox", { name: label("machine.capability_restic_restore") });
  await expect(right).not.toBeChecked();
  await right.check();
  const note = label("machine.capability_restic_restore_note").split("{product}")[0]?.slice(0, 40) ?? "";
  await expect(dialog).toContainText(note);
  await right.uncheck();
  await expect(dialog.getByText(note)).toHaveCount(0);
  expect(findings.unknown()).toEqual([]);
});
