import type { Locator, Page } from "@playwright/test";

import { label } from "./support/catalog";
import { expect, test } from "./support/findings";

// La page Sauvegardes de B2, contre les données fictives : un service entier
// est son dossier, ses volumes et ses bases. paperless (docker1) les a tous
// les trois et sa base a échoué cette nuit ; photos (docker1) a un volume et
// rien ne le sauvegarde ; carnet (pve-nord) a une base, mais pve-nord n'a pas
// le droit restic.docker ; Proxmox sauvegarde encore ses VM de lui-même (la
// tâche nuit-pi) : la page dit qu'elle est à remplacer. Chaque bouton est
// cliqué, chaque assistant mené au bout. Les tests se suivent : ils partagent
// le serveur.

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

test.describe.configure({ mode: "serial" });

test("un service entier : son dossier, son volume, sa base ; la partie en échec et la dernière réussite se lisent", async ({ page, findings }) => {
  findings.where = "/sauvegardes › service entier";
  await openBackups(page);
  const paperless = card(page, "paperless");
  const parts = paperless.locator(".backup-parts");
  await expect(parts).toContainText(label("backup.parts_has"));
  await expect(parts.locator(".backup-part")).toHaveCount(3);
  await expect(parts).toContainText(label("backup.part_folder"));
  await expect(parts).toContainText("paperless_data");
  await expect(parts).toContainText("paperless-db-1");
  await expect(parts).toContainText(filled("backup.part_database", label("backup.part_engine_postgres")));
  // La base a échoué : sa raison est dite, les deux autres parties sont gardées.
  await expect(parts.locator(".backup-part-failed")).toHaveCount(1);
  await expect(parts.locator(".backup-part-ok")).toHaveCount(2);
  await expect(parts).toContainText(label("action.reason_dump_failed"));
  await expect(paperless.locator(".pill", { hasText: label("backup.status_failed") }).first()).toBeVisible();
  // Le dernier essai et la dernière réussite sont deux faits.
  await expect(paperless).toContainText(label("backup.fact_last_attempt"));
  await expect(paperless).toContainText(label("backup.fact_last_success"));

  // Un service qu'aucune sauvegarde ne couvre dit ce qui partira.
  const photos = card(page, "photos");
  await expect(photos.locator(".backup-parts")).toContainText(label("backup.parts_will"));
  await expect(photos.locator(".backup-parts")).toContainText("photos_data");
  await expect(photos.locator(".backup-part-mark.backup-part-ok")).toHaveCount(0);
  expect(findings.unknown()).toEqual([]);
});

test("la tâche Proxmox nuit-pi est observée, à remplacer : le bouton ouvre l'assistant sur la VM", async ({ page, findings }) => {
  findings.where = "/sauvegardes › à remplacer";
  await openBackups(page);
  const mail = card(page, "mail");
  await expect(mail).toContainText(label("backup.origin_observed"));
  await expect(mail).toContainText(label("backup.replace_title").replace("{product}", ""));
  await mail.getByRole("link", { name: label("backup.replace_button").replace("{product}", "") }).click();
  await expect(page).toHaveURL(/\/sauvegardes\/nouvelle\?source=/);
  const dialog = page.locator("dialog[open]");
  await expect(dialog.getByRole("radiogroup", { name: label("backup.restic_scope_label") }).getByRole("radio", { name: label("backup.restic_scope_vm") })).toBeChecked();
  await expect(dialog).toContainText(label("backup.restic_scope_note_vm").split("{product}")[1] ?? "");
  await dialog.getByRole("button", { name: label("modal.close") }).first().click();
  await expect(dialog).toHaveCount(0);
  expect(findings.unknown()).toEqual([]);
});

test("un service avec une base sur une machine sans restic.docker : le droit se dit en geste, coché d'avance", async ({ page, findings }) => {
  findings.where = "/sauvegardes › restic.docker manquant";
  await openBackups(page);
  const carnet = card(page, "carnet");
  await expect(carnet.locator(".backup-parts")).toContainText("carnet-db-1");
  await expect(carnet).toContainText(filled("backup.unfit_right_missing", "pve-nord", "restic.docker"));
  await carnet.getByRole("link", { name: filled("machine.reenroll_with", label("machine.capability_restic_docker")) }).click();
  await expect(page).toHaveURL(/\/machines\/[^/]+\/reenroller\?droit=restic\.docker/);
  const dialog = page.locator("dialog[open]");
  await expect(dialog.getByRole("checkbox", { name: label("machine.capability_restic_docker") })).toBeChecked();
  await expect(dialog).toContainText(label("machine.capability_restic_docker_note"));
  expect(findings.unknown()).toEqual([]);
});

test("assistant : sauvegarder un service entier avec son volume, de bout en bout", async ({ page, findings }) => {
  findings.where = "/sauvegardes/nouvelle (service entier)";
  await openBackups(page);
  await page.goto("/sauvegardes/nouvelle?source=" + encodeURIComponent(await sourceKey(page, "photos")));
  const dialog = page.locator("dialog[open]");
  // Quoi : ce qui part avec le service, et le coût dit.
  await expect(dialog.getByRole("radio", { name: /^photos/ })).toBeChecked();
  await expect(dialog.locator(".backup-parts")).toContainText("photos_data");
  await expect(dialog).toContainText(label("backup.restic_scope_note_service").slice(0, 40));
  await dialog.getByRole("button", { name: label("wizard.next") }).click();
  await dialog.getByRole("button", { name: label("wizard.next") }).click();
  await dialog.getByRole("button", { name: label("backup.restic_submit") }).click();
  await expect(dialog).toHaveCount(0);
  await expect(card(page, "photos")).toContainText(label("backup.status_never"));
  expect(findings.unknown()).toEqual([]);
});

test("accéder : la base et le volume d'un service entier se restaurent, commandes à copier", async ({ page, findings }) => {
  findings.where = "/sauvegardes › accéder (base, volume)";
  await openBackups(page);
  const access = page.locator("section[aria-labelledby=backup-access-title]");
  await access.getByRole("combobox", { name: label("backup.access_pick") }).click();
  await page.getByRole("option", { name: /^paperless/ }).click();
  await expect(access).toContainText(`${label("backup.access_restore_database")} · paperless-db-1`);
  await expect(access).toContainText(`${label("backup.access_restore_volume")} · paperless_data`);
  await expect(access).toContainText("database-paperless-db-1.sql");
  await expect(access).toContainText("volume-paperless_data.tar");
  await expect(access).toContainText('psql -U "$POSTGRES_USER"');
  await access.getByRole("button", { name: label("machine.copy") }).last().click();
  await expect(access.getByRole("button", { name: new RegExp(`${label("machine.copied")}|${label("machine.copy_failed")}`) }).first()).toBeVisible();
  expect(findings.unknown()).toEqual([]);
});

test("ajouter une machine : le droit restic.docker se coche, avec sa note, et part dans la ligne d'installation", async ({ page, findings }) => {
  findings.where = "/machines/nouvelle (restic.docker)";
  await page.goto("/machines/nouvelle");
  const dialog = page.locator("dialog[open]");
  await dialog.getByLabel(label("machine.name_label")).fill("vps-essai-2");
  await dialog.getByText(label("machine.rights_advanced")).click();
  const right = dialog.getByLabel(label("machine.capability_restic_docker"));
  await expect(right).not.toBeChecked();
  await right.check();
  await expect(dialog).toContainText(label("machine.capability_restic_docker_note"));
  await dialog.getByRole("button", { name: label("machine.create_token") }).click();
  await expect(dialog.locator(".token-box", { hasText: "restic.docker" }).first()).toBeVisible();
  await dialog.locator(".modal-foot").getByRole("button", { name: label("modal.close") }).click();
  expect(findings.unknown()).toEqual([]);
});

// La clé d'une source de l'aperçu, lue comme l'écran la lit.
async function sourceKey(page: Page, name: string): Promise<string> {
  const response = await page.request.get("/api/backups/overview");
  const overview = (await response.json()) as { sources: { key: string; name: string }[] };
  const found = overview.sources.find((source) => source.name === name);
  if (found === undefined) {
    throw new Error(`source ${name} absente de l'aperçu`);
  }
  return found.key;
}
