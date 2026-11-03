import type { Locator, Page } from "@playwright/test";

import { label } from "./support/catalog";
import { expect, test } from "./support/findings";

// La page Sauvegardes de B1, contre les données fictives : pi est une cible
// restic en marche ; paperless (docker1) part en direct, vaultwarden
// (pve-nord) par le relais ; Proxmox sauvegarde ses VM de lui-même (la tâche
// nuit-pi) ; il manque des droits à d'autres machines ; coffre (arm64) est
// une cible sans rest-server, atelier une source sans restic. Chaque bouton
// est cliqué, chaque menu ouvert avec un choix, chaque assistant mené au
// bout : une sauvegarde de service (refusée sous le plancher, puis reprise),
// une de VM, une cible. Les tests se suivent : ils partagent le serveur.

// Un libellé du catalogue rempli de ses valeurs, comme le fait le front.
function filled(key: string, ...args: (string | number)[]): string {
  return args.reduce<string>((text, value) => text.replace(/%[sd]/, String(value)), label(key));
}

// Le bloc « Installer … sur … » d'une machine, où qu'il soit.
function installBlock(scope: Page | Locator, software: string, machine: string): Locator {
  return scope.getByRole("group", { name: filled("backup.install_title", software, machine) });
}

function card(page: Page, name: string): Locator {
  return page.locator("article.backup-source").filter({ has: page.locator("h3", { hasText: new RegExp(`^${name}$`) }) });
}

async function openBackups(page: Page): Promise<void> {
  await page.clock.setFixedTime(new Date("2026-09-12T12:00:00Z"));
  await page.goto("/sauvegardes");
  await expect(page.locator(".backup-verdict")).toBeVisible();
}

test.describe.configure({ mode: "serial" });

test("la page : verdict, cartes, chemin du relais à copier, cible et filtres", async ({ page, findings }) => {
  findings.where = "/sauvegardes";
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
  await openBackups(page);

  // Une carte par source, aucun tableau pour la liste principale.
  await expect(page.locator("main .backup-grid table")).toHaveCount(0);
  await expect(card(page, "paperless")).toContainText(label("backup.path_direct_pill"));
  // La rétention, appliquée par la cible chaque nuit ; pas encore de purge.
  await expect(card(page, "paperless")).toContainText(`${filled("backup.retention_daily", 14)} · ${filled("backup.retention_weekly", 8)} · ${filled("backup.retention_monthly", 3)}`);
  await expect(card(page, "paperless")).toContainText(filled("backup.retention_by_target", "pi"));
  await expect(card(page, "paperless")).toContainText(label("backup.forget_none"));

  // Le relais : la raison, et ce qu'il faut ouvrir, à copier tel quel.
  const relay = card(page, "vaultwarden");
  await expect(relay).toContainText(label("backup.path_relay_pill"));
  const hint = filled("backup.path_direct_hint", "pve-nord", "192.0.2.10:8000");
  await expect(relay.locator(".token-box code")).toHaveText(hint);
  await relay.getByRole("button", { name: label("machine.copy") }).click();
  await expect(relay.getByRole("button", { name: new RegExp(`${label("machine.copied")}|${label("machine.copy_failed")}`) })).toBeVisible();

  // Proxmox de lui-même : la tâche nuit-pi et sa rétention en clair.
  const observed = card(page, "web");
  await expect(observed).toContainText(label("backup.origin_observed"));
  await expect(observed).toContainText(filled("backup.origin_job", "nuit-pi"));
  await expect(observed).toContainText(`${filled("backup.retention_daily", 14)} · ${filled("backup.retention_weekly", 8)} · ${filled("backup.retention_monthly", 3)}`);

  // Les filtres : à traiter, à jour, tout.
  const filters = page.getByRole("radiogroup", { name: label("backup.filter_label") });
  await filters.getByRole("radio", { name: new RegExp(`^${label("backup.filter_attention")}`) }).check();
  await expect(card(page, "paperless")).toHaveCount(0);
  await expect(card(page, "photos")).toBeVisible();
  await filters.getByRole("radio", { name: new RegExp(`^${label("backup.filter_protected")}`) }).check();
  await expect(card(page, "photos")).toHaveCount(0);
  await filters.getByRole("radio", { name: new RegExp(`^${label("backup.filter_all")}`) }).check();
  await expect(card(page, "photos")).toBeVisible();

  // La cible : son disque, son rest-server ; la retirer est grisé, des sauvegardes y vont.
  const target = page.locator("article.backup-target", { has: page.locator("h3", { hasText: /^pi$/ }) });
  await expect(target).toContainText(label("backup.target_state_running"));
  await expect(target).toContainText("192.0.2.10:8000");
  await expect(target).toContainText(filled("backup.target_floor", "pi", filled("backup.retention_daily", 7), "pi"));
  await expect(target).toContainText(filled("backup.target_purge_by", "pi"));
  await target.getByRole("button", { name: filled("entity.more", "pi") }).click();
  const remove = page.getByRole("menuitem", { name: new RegExp(label("backup.target_remove")) });
  await expect(remove).toHaveAttribute("aria-disabled", "true");
  await page.keyboard.press("Escape");

  // La base d'openCloud est une ligne de plus : sa clé et ses destinations sont sur sa carte.
  const database = card(page, label("backup.source_database").replace("{product}", "openCloud"));
  await expect(database).toContainText(label("backup.keyring_title"));
  await expect(database).toContainText(label("backup.destination_title"));
  await expect(page.locator("#archives")).toHaveCount(0);
  expect(findings.unknown()).toEqual([]);
});

test("binaire manquant : la cible et la source nommées, les commandes à copier", async ({ page, findings }) => {
  findings.where = "/sauvegardes › binaire manquant";
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
  await openBackups(page);
  const copied = new RegExp(`${label("machine.copied")}|${label("machine.copy_failed")}`);

  // coffre est une cible sans rest-server : les commandes de son architecture, évaluées.
  const coffre = page.locator("article.backup-target", { has: page.locator("h3", { hasText: /^coffre$/ }) });
  await expect(coffre).toContainText(filled("backup.target_rest_server_missing", "coffre"));
  const server = installBlock(coffre, "rest-server", "coffre");
  await expect(server).toContainText(filled("backup.install_rest_server_detail", "arm64", "0.14.0", "coffre"));
  await expect(server.locator("code")).toHaveText([
    "curl -fsSLO https://github.com/restic/rest-server/releases/download/v0.14.0/rest-server_0.14.0_linux_arm64.tar.gz",
    "curl -fsSLO https://github.com/restic/rest-server/releases/download/v0.14.0/SHA256SUMS",
    "sha256sum -c --ignore-missing SHA256SUMS",
    "tar -xzf rest-server_0.14.0_linux_arm64.tar.gz",
    "sudo install -m 0755 rest-server_0.14.0_linux_arm64/rest-server /usr/local/bin/rest-server",
  ]);
  await expect(server.locator(".backup-on").first()).toHaveText("coffre");
  await server.getByRole("button", { name: label("machine.copy") }).first().click();
  await expect(server.getByRole("button", { name: copied }).first()).toBeVisible();
  await expect(coffre).toContainText(filled("backup.target_floor_unknown", "coffre"));

  // grimoire, sur atelier qui n'a pas restic : la carte dit quoi installer, et où.
  const grimoire = card(page, "grimoire");
  await expect(grimoire).toContainText(filled("backup.unfit_restic_missing", "atelier"));
  const restic = installBlock(grimoire, "restic", "atelier");
  await expect(restic.locator("code")).toHaveText(["sudo apt install restic"]);
  await restic.getByRole("button", { name: label("machine.copy") }).click();
  await expect(restic.getByRole("button", { name: copied })).toBeVisible();
  expect(findings.unknown()).toEqual([]);
});

test("un droit manquant se dit en geste : « Ré-enrôler avec ce droit », coché d'avance", async ({ page, findings }) => {
  findings.where = "/sauvegardes › droit manquant";
  await openBackups(page);
  const missing = card(page, "nextcloud");
  await expect(missing).toContainText(filled("backup.unfit_right_missing", "vps-paris-1", "restic.produce"));
  await missing.getByRole("link", { name: filled("machine.reenroll_with", label("machine.capability_restic_produce")) }).click();
  await expect(page).toHaveURL(/\/machines\/[^/]+\/reenroller\?droit=restic\.produce/);
  await expect(page.locator("dialog[open]").getByRole("checkbox", { name: label("machine.capability_restic_produce") })).toBeChecked();
  expect(findings.unknown()).toEqual([]);
});

test("menu d'une sauvegarde restic : la lancer maintenant, puis modifier sa rétention, refusée sous le plancher", async ({ page, findings }) => {
  findings.where = "/sauvegardes › paperless";
  await openBackups(page);
  const paperless = card(page, "paperless");
  await paperless.getByRole("button", { name: filled("entity.more", "paperless") }).click();
  await page.getByRole("menuitem", { name: label("backup.run_now") }).click();
  await expect(paperless.locator(".pill", { hasText: label("backup.status_running") }).first()).toBeVisible();

  await paperless.getByRole("button", { name: filled("entity.more", "paperless") }).click();
  await page.getByRole("menuitem", { name: label("backup.restic_edit") }).click();
  await expect(page).toHaveURL(/\/sauvegardes\/restic\/[^/]+\/[^/]+\/modifier$/);
  const dialog = page.locator("dialog[open]");
  await expect(dialog).toContainText(filled("backup.retention_floor_of", "pi", filled("backup.retention_daily", 7)));
  // Sans baisse, aucune saisie du nom : un renommage seul s'enregistre.
  await expect(dialog.locator("#restic-confirm-name")).toHaveCount(0);
  // Baisser : le nom de la sauvegarde est exigé, « Enregistrer » attend.
  const save = dialog.getByRole("button", { name: label("backup.plan_save") });
  await dialog.locator("#restic-keep_daily").fill("3");
  await expect(dialog.locator("#restic-confirm-name")).toBeVisible();
  await expect(save).toBeDisabled();
  await dialog.locator("#restic-confirm-name").fill("Paperless");
  await expect(save).toBeDisabled();
  await dialog.locator("#restic-confirm-name").fill("paperless");
  await expect(save).toBeEnabled();
  // Sous le plancher de pi : le serveur refuse, en le disant.
  await save.click();
  await expect(dialog).toContainText(label("restic.retention_below_floor"));
  await dialog.locator("#restic-keep_daily").fill("7");
  await save.click();
  await expect(dialog).toHaveCount(0);
  await expect(card(page, "paperless")).toContainText(filled("backup.retention_daily", 7));
  expect(findings.unknown()).toEqual([]);
});

test("une hausse ne demande pas le nom ; la baisse faite ici est au journal", async ({ page, findings }) => {
  findings.where = "/sauvegardes › vaultwarden › journal";
  await openBackups(page);
  const editRetention = async () => {
    await card(page, "vaultwarden").getByRole("button", { name: filled("entity.more", "vaultwarden") }).click();
    await page.getByRole("menuitem", { name: label("backup.restic_edit") }).click();
    return page.locator("dialog[open]");
  };

  // Une hausse : pas de saisie du nom, « Enregistrer » est tout de suite prêt.
  let dialog = await editRetention();
  await dialog.locator("#restic-keep_monthly").fill("24");
  await expect(dialog.locator("#restic-confirm-name")).toHaveCount(0);
  await expect(dialog.getByRole("button", { name: label("backup.plan_save") })).toBeEnabled();
  await dialog.getByRole("button", { name: label("backup.plan_save") }).click();
  await expect(dialog).toHaveCount(0);

  // Une baisse, faite par ce test : le nom est exigé, puis la ligne est au journal.
  dialog = await editRetention();
  await dialog.locator("#restic-keep_monthly").fill("12");
  await dialog.locator("#restic-confirm-name").fill("vaultwarden");
  await dialog.getByRole("button", { name: label("backup.plan_save") }).click();
  await expect(dialog).toHaveCount(0);
  await page.goto("/journal");
  await expect(page.locator("tbody tr", { hasText: label("audit.verb_retention_lower") }).filter({ hasText: "vaultwarden" }).first()).toBeVisible();
  expect(findings.unknown()).toEqual([]);
});

test("assistant : sauvegarder un service, de bout en bout", async ({ page, findings }) => {
  findings.where = "/sauvegardes/nouvelle (service)";
  await openBackups(page);
  await page.getByRole("link", { name: label("backup.save_ellipsis") }).click();
  await expect(page).toHaveURL(/\/sauvegardes\/nouvelle$/);
  const dialog = page.locator("dialog[open]");

  // Quoi : la recherche garde photos ; un service sans droit est grisé, avec
  // son geste ; grimoire aussi, faute de restic sur atelier, avec ses commandes.
  await expect(dialog.locator(".pick-row-off", { hasText: "nextcloud" })).toContainText(label("machine.reenroll_with_right"));
  await expect(dialog.locator(".pick-row-off", { hasText: "grimoire" })).toContainText(filled("backup.unfit_restic_missing", "atelier"));
  await installBlock(dialog, "restic", "atelier").getByRole("button", { name: label("machine.copy") }).click();
  await dialog.getByRole("searchbox", { name: label("backup.restic_search_label") }).fill("pho");
  await dialog.getByRole("radio", { name: /^photos/ }).check();
  await expect(dialog.getByRole("radio", { name: /photos/ })).toBeChecked();
  await dialog.getByRole("button", { name: label("wizard.next") }).click();

  // Où : pi est prête et choisie ; docker1 est la source, grisée ; coffre
  // aussi, faute de rest-server, avec les commandes à copier.
  await expect(dialog.getByRole("radio", { name: /^pi/ })).toBeChecked();
  await expect(dialog.locator(".pick-row-off", { hasText: "docker1" })).toContainText(label("backup.unfit_same_machine"));
  await expect(dialog.locator(".pick-row-off", { hasText: "coffre" })).toContainText(label("backup.unfit_rest_server_missing"));
  await installBlock(dialog, "rest-server", "coffre").getByRole("button", { name: label("machine.copy") }).last().click();
  // La rétention se règle ; sous le plancher de pi, elle sera refusée.
  await dialog.locator("summary", { hasText: label("wizard.settings") }).click();
  await expect(dialog).toContainText(filled("backup.retention_floor_of", "pi", filled("backup.retention_daily", 7)));
  await dialog.locator("#restic-keep_daily").fill("3");
  await dialog.getByRole("button", { name: label("wizard.next") }).click();

  // Récapitulatif : le chemin attendu est celui de paperless, en direct ; la
  // rétention appliquée par pi ; les mots de passe, jamais tapés.
  const summary = dialog.locator(".wizard-summary");
  await expect(summary).toContainText(label("backup.restic_summary_path"));
  await expect(summary).toContainText(label("backup.path_direct_pill"));
  await expect(summary).toContainText(filled("backup.retention_applied", "pi"));
  await expect(summary).toContainText(label("backup.restic_summary_passwords"));
  await expect(summary).toContainText(label("backup.restic_passwords").split("{product}")[1] ?? "");
  await dialog.getByRole("button", { name: label("backup.restic_submit") }).click();
  await expect(dialog.getByRole("alert")).toContainText(label("restic.retention_below_floor"));

  // Retour à « Où », la rétention par défaut, puis droit au récapitulatif.
  await dialog.getByRole("button", { name: filled("wizard.edit_step", label("backup.restic_step_where")) }).click();
  await dialog.locator("#restic-keep_daily").fill("14");
  await dialog.getByRole("button", { name: label("wizard.back_to_summary") }).click();
  await expect(summary).toContainText(filled("backup.retention_daily", 14));
  await dialog.getByRole("button", { name: label("backup.restic_submit") }).click();
  await expect(dialog).toHaveCount(0);
  await expect(card(page, "photos")).toContainText(label("backup.status_never"));
  expect(findings.unknown()).toEqual([]);
});

test("assistant : sauvegarder une VM entière, de bout en bout, puis y accéder", async ({ page, findings }) => {
  findings.where = "/sauvegardes/nouvelle (VM)";
  await openBackups(page);
  await page.goto("/sauvegardes/nouvelle");
  const dialog = page.locator("dialog[open]");
  await dialog.getByRole("radiogroup", { name: label("backup.restic_scope_label") }).getByRole("radio", { name: label("backup.restic_scope_vm") }).check();
  // L'hôte pve-nord n'a pas backup.produce : ses VM sont grisées, avec le geste.
  const grayed = dialog.locator(".pick-row-off", { hasText: "mail" });
  await expect(grayed).toContainText(filled("backup.unfit_right_missing", "pve-nord", "backup.produce"));
  await expect(grayed.getByRole("link", { name: filled("machine.reenroll_with", label("machine.capability_backup_produce")) })).toBeVisible();
  await dialog.getByRole("radio", { name: /^web/ }).check();
  await dialog.getByRole("button", { name: label("wizard.next") }).click();
  await dialog.getByRole("button", { name: label("wizard.next") }).click();
  await expect(dialog.locator(".wizard-summary")).toContainText(filled("backup.restic_what_vm", "pve"));
  await dialog.getByRole("button", { name: label("backup.restic_submit") }).click();
  await expect(dialog).toHaveCount(0);
  await expect(card(page, "web")).toContainText(label("backup.origin_restic_vm").slice(0, 10));

  // Accéder : la VM choisie, la commande de restauration en VM neuve.
  const access = page.locator("section[aria-labelledby=backup-access-title]");
  await access.getByRole("combobox", { name: label("backup.access_pick") }).click();
  await page.getByRole("option", { name: /^web/ }).click();
  await expect(access).toContainText(filled("backup.access_password_note", "pve"));
  await expect(access).toContainText(label("backup.access_password_kept").split("{product}")[0] ?? "");
  await expect(access).toContainText(filled("backup.access_run_from", "pve"));
  await expect(access).toContainText(label("backup.access_restore_vm"));
  await expect(access).toContainText("qmrestore - 6012");
  await expect(access).toContainText(label("backup.access_observed"));
  await access.getByRole("button", { name: label("machine.copy") }).first().click();
  expect(findings.unknown()).toEqual([]);
});

test("faire d'une machine une cible : le menu grise ce qui ne convient pas, nas le devient", async ({ page, findings }) => {
  findings.where = "/sauvegardes/cibles/nouvelle";
  await openBackups(page);
  await page.getByRole("link", { name: label("backup.make_target") }).first().click();
  const dialog = page.locator("dialog[open]");
  await expect(dialog.locator("#restic-target-dir")).toHaveValue("/srv/restic");
  await dialog.locator("#restic-target-machine").click();
  const list = page.locator("[role=listbox]").last();
  await expect(list.locator("[role=option][aria-disabled=true]", { hasText: "pi" }).first()).toBeVisible();
  await expect(list).toContainText(label("backup.unfit_already_target"));
  await list.locator("[role=option]:not([aria-disabled=true])", { hasText: "nas" }).click();
  await dialog.locator("#restic-target-host").fill("192.0.2.20");
  // Le plancher : sept nuits d'avance, relevé à dix.
  await expect(dialog.locator("#restic-target-floor")).toHaveValue("7");
  await dialog.locator("#restic-target-floor").fill("10");
  await expect(dialog).toContainText(filled("backup.target_setup_purge", "nas"));
  await dialog.getByRole("button", { name: filled("backup.target_setup_submit", "nas") }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page.locator("article.backup-target", { has: page.locator("h3", { hasText: /^nas$/ }) })).toContainText(label("backup.target_state_pending"));
  expect(findings.unknown()).toEqual([]);
});

test("supprimer une sauvegarde restic : la confirmation, puis la source redevient à protéger", async ({ page, findings }) => {
  findings.where = "/sauvegardes › supprimer";
  await openBackups(page);
  const photos = card(page, "photos");
  await photos.getByRole("button", { name: filled("entity.more", "photos") }).click();
  await page.getByRole("menuitem", { name: label("backup.restic_delete") }).click();
  const dialog = page.locator("dialog[open]");
  await expect(dialog).toContainText(filled("backup.restic_delete_confirm", "photos", "pi"));
  await dialog.locator(".modal-foot").getByRole("button", { name: label("backup.restic_delete") }).click();
  await expect(card(page, "photos")).toContainText(label("backup.status_none"));
  expect(findings.unknown()).toEqual([]);
});

test("la clé des archives : après la création, la fenêtre rappelle de noter la phrase (F-6)", async ({ page, findings }) => {
  findings.where = "/sauvegardes/cle";
  await page.goto("/sauvegardes/cle");
  const dialog = page.locator("dialog[open]");
  await dialog.locator("#backup-passphrase").fill("une phrase de passe assez longue");
  await dialog.locator("#backup-passphrase-confirm").fill("une phrase de passe assez longue");
  await dialog.getByRole("button", { name: label("backup.keyring_replace") }).click();
  await page.locator("dialog[open]").last().getByRole("button", { name: label("backup.keyring_replace") }).last().click();
  const created = page.locator("dialog[open]");
  await expect(created).toContainText(label("backup.passphrase_note_now"));
  await created.getByRole("button", { name: label("backup.keyring_noted") }).click();
  await expect(page).toHaveURL(/\/sauvegardes$/);
  expect(findings.unknown()).toEqual([]);
});
