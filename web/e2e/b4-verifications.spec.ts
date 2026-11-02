import type { Locator, Page } from "@playwright/test";

import { label } from "./support/catalog";
import { expect, test } from "./support/findings";

// La page Sauvegardes de B4, contre les données fictives : la supervision vit
// dans le module. Le dépôt docker1 → pi (paperless, recettes, agenda) a été
// vérifié par pi ; celui de pve-nord → pi (vaultwarden, créé il y a vingt-trois
// jours) n'a jamais été testé ; agenda n'a pas réussi depuis quatre jours ;
// calendrier est gardé par stockage, qui n'a pas le droit restic.verify. Les services
// sans plan (photos, carnet, la base d'openCloud) sont des sources sans plan.
// Chaque bouton est cliqué, chaque menu ouvert, chaque lien suivi. Les tests se
// suivent : ils partagent le serveur.

function filled(key: string, ...args: (string | number)[]): string {
  return args.reduce<string>((text, value) => text.replace(/%[sd]/, String(value)), label(key));
}

// La phrase d'un catalogue, sans ses arguments : de quoi la retrouver à l'écran.
function lead(key: string): string {
  return label(key).split("%")[0]?.trim() ?? "";
}

function card(page: Page, name: string): Locator {
  return page.locator("article.backup-source").filter({ has: page.locator("h3", { hasText: new RegExp(`^${name}$`) }) });
}

function watch(page: Page): Locator {
  return page.locator("section.card").filter({ has: page.locator(".card-t", { hasText: label("backup.watch_title") }) });
}

async function openBackups(page: Page): Promise<void> {
  await page.clock.setFixedTime(new Date("2026-09-12T12:00:00Z"));
  await page.goto("/sauvegardes");
  await expect(page.locator(".backup-verdict")).toBeVisible();
}

test.describe.configure({ mode: "serial" });

test("la surveillance : chaque alerte de sauvegarde se lit sur la page, avec son geste", async ({ page, findings }) => {
  findings.where = "/sauvegardes › surveillance";
  await openBackups(page);
  const section = watch(page);
  await expect(section).toBeVisible();
  await expect(section.locator(".pill").first()).toContainText(lead("backup.watch_open"));
  const row = (fact: string) => section.locator(".alert-row", { hasText: fact });
  // Aucune réussite depuis quatre jours : agenda.
  const stale = row(lead("alert.fact_backup_stale"));
  await expect(stale).toHaveCount(1);
  await expect(stale).toContainText("agenda");
  await expect(stale).toContainText(lead("alert.action_backup_stale").slice(0, 30));
  await expect(stale.getByRole("button", { name: label("alert.acknowledge") })).toBeVisible();
  // Test de restauration périmé : le dépôt de pve-nord (jamais testé) et celui de
  // stockage (la cible ne peut pas), un par dépôt.
  const restore = row(lead("alert.fact_restore_test_stale"));
  await expect(restore).toHaveCount(2);
  await expect(restore.filter({ hasText: "pve-nord → pi" })).toContainText(label("alert.action_restore_test_stale"));
  await expect(restore.filter({ hasText: "docker1 → stockage" })).toContainText(label("alert.action_restore_test_stale_right"));
  // Source sans plan : photos, carnet et la base d'openCloud au moins.
  const unprotected = row(label("alert.fact_source_unprotected"));
  expect(await unprotected.count()).toBeGreaterThanOrEqual(3);
  await expect(unprotected.filter({ hasText: "photos" })).toHaveCount(1);
  await expect(unprotected.filter({ hasText: "carnet" })).toHaveCount(1);
  expect(findings.unknown()).toEqual([]);
});

test("une alerte de sauvegarde s'ouvre en fiche, et son objet mène à la page Sauvegardes", async ({ page, findings }) => {
  findings.where = "/sauvegardes › fiche d'une alerte";
  await openBackups(page);
  const stale = watch(page).locator(".alert-row", { hasText: lead("alert.fact_backup_stale") });
  await stale.getByRole("link", { name: new RegExp(`^${lead("alert.fact_backup_stale")}`) }).click();
  await expect(page).toHaveURL(/\/alertes\/alerte\/\d+/);
  await expect(page.locator(".alert-row")).toContainText("agenda");
  await page.locator(".alert-row .aside").getByRole("link", { name: "agenda" }).click();
  await expect(page).toHaveURL(/\/sauvegardes$/);
  await expect(page.locator(".backup-verdict")).toBeVisible();
  expect(findings.unknown()).toEqual([]);
});

test("le test de restauration se lit sur la carte : réussi, jamais testé, périmé", async ({ page, findings }) => {
  findings.where = "/sauvegardes › test de restauration";
  await openBackups(page);
  const paperless = card(page, "paperless");
  await expect(paperless).toContainText(label("backup.fact_verify"));
  await expect(paperless).toContainText(lead("backup.verify_succeeded"));
  await expect(paperless).toContainText(filled("backup.verify_read", filled("backup.verify_files", 4), filled("backup.verify_snapshots", 2)));

  // Le dépôt est celui de la source vers la cible : recettes et agenda partagent
  // celui de paperless.
  await expect(card(page, "recettes")).toContainText(lead("backup.verify_succeeded"));

  // Créé il y a vingt-trois jours, jamais testé : l'avertissement le dit.
  const vaultwarden = card(page, "vaultwarden");
  await expect(vaultwarden).toContainText(label("backup.verify_never"));
  await expect(vaultwarden).toContainText(label("backup.verify_never_sub"));
  await expect(vaultwarden.locator(".backup-item-warn").first()).toBeVisible();
  expect(findings.unknown()).toEqual([]);
});

test("le menu d'une carte offre « Vérifier maintenant » : le test part, la carte dit qu'il court", async ({ page, findings }) => {
  findings.where = "/sauvegardes › vérifier maintenant";
  await openBackups(page);
  // intranet (bureau → pi) a son dépôt à lui : aucune sauvegarde lancée plus tôt ne l'occupe.
  const intranet = card(page, "intranet");
  await intranet.getByRole("button", { name: filled("entity.more", "intranet") }).click();
  const menu = page.getByRole("menu");
  await expect(menu).toBeVisible();
  await expect(menu.getByRole("menuitem")).toHaveText([label("backup.browse"), label("backup.run_now"), label("backup.verify_now"), label("backup.restic_edit"), label("backup.restic_delete")]);
  await menu.getByRole("menuitem", { name: label("backup.verify_now") }).click();
  await expect(intranet).toContainText(label("backup.verify_sent"));
  expect(findings.unknown()).toEqual([]);
});

test("la cible sans restic.verify : la carte le dit, le geste ré-enrôle avec ce droit, le menu refuse avec le même geste", async ({ page, findings }) => {
  findings.where = "/sauvegardes › cible sans restic.verify";
  await openBackups(page);
  const calendrier = card(page, "calendrier");
  await expect(calendrier).toContainText(filled("backup.verify_right_missing", "stockage"));
  // Le menu : le serveur refuse, la note porte le même geste, sur la cible.
  await calendrier.getByRole("button", { name: filled("entity.more", "calendrier") }).click();
  await page.getByRole("menu").getByRole("menuitem", { name: label("backup.verify_now") }).click();
  await expect(calendrier).toContainText(label("restic.target_verify_right_missing"));
  const gestures = calendrier.getByRole("link", { name: filled("machine.reenroll_with", label("machine.capability_restic_verify")) });
  await expect(gestures).toHaveCount(2);
  await gestures.first().click();
  await expect(page).toHaveURL(/\/machines\/[^/]+\/reenroller\?droit=restic\.verify/);
  const dialog = page.locator("dialog[open]");
  await expect(dialog.getByRole("checkbox", { name: label("machine.capability_restic_verify") })).toBeChecked();
  await expect(dialog).toContainText(label("machine.capability_restic_verify_note").split("{product}")[0]?.slice(0, 40) ?? "");
  await dialog.getByRole("button", { name: label("modal.close") }).first().click();
  await expect(dialog).toHaveCount(0);
  expect(findings.unknown()).toEqual([]);
});

test("l'enrôlement offre le droit « Vérifier les dépôts reçus », décoché d'avance, avec sa note", async ({ page, findings }) => {
  findings.where = "/machines/nouvelle › restic.verify";
  await page.goto("/machines/nouvelle");
  const dialog = page.locator("dialog[open]");
  await dialog.getByText(label("machine.rights_advanced")).click();
  const right = dialog.getByRole("checkbox", { name: label("machine.capability_restic_verify") });
  await expect(right).not.toBeChecked();
  await right.check();
  await expect(dialog).toContainText(label("machine.capability_restic_verify_note").split("{product}")[0]?.slice(0, 40) ?? "");
  await right.uncheck();
  await expect(dialog.getByText(label("machine.capability_restic_verify_note").split("{product}")[0]?.slice(0, 40) ?? "")).toHaveCount(0);
  expect(findings.unknown()).toEqual([]);
});

test("les alertes de sauvegarde ouvertes se filtrent par type dans la boîte des alertes", async ({ page, findings }) => {
  findings.where = "/alertes › genres de sauvegarde";
  await page.goto("/alertes");
  await page.getByRole("combobox", { name: label("alerts.filter_kind") }).click();
  const options = page.getByRole("option");
  for (const kind of ["backup_stale", "restore_test_stale", "source_unprotected"]) {
    await expect(options.filter({ hasText: label(`alert.kind_${kind}`) })).toHaveCount(1);
  }
  await options.filter({ hasText: label("alert.kind_source_unprotected") }).click();
  await expect(page).toHaveURL(/genre=source_unprotected/);
  await expect(page.locator(".alert-row").first()).toContainText(label("alert.fact_source_unprotected"));
  expect(findings.unknown()).toEqual([]);
});
