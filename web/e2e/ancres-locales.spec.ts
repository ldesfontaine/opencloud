import { label } from "./support/catalog";
import { expect, test } from "./support/findings";

// Les ancres de confiance locales (lot 1 de « compromission du serveur »), sur
// les données fictives : pve-ancres n'a aucune ancre posée, pve-illisible a un
// fichier dont les valeurs ne se lisent pas, pve (comme toutes les autres) a un
// agent d'avant les ancres. Chaque écran est cliqué : la fiche, la fenêtre
// « Ré-enrôler » avec ses champs et sa ligne d'installation, le refus d'un
// ordre avec la commande à lancer sur la machine.

const noneMachine = "aaaaaaaa-1111-4222-8333-444444444444";
const brokenMachine = "bbbbbbbb-1111-4222-8333-555555555555";

function anchorsCard(page: import("@playwright/test").Page) {
  return page.locator("section.agent-anchors");
}

test("la fiche dit les deux états : aucune ancre posée, ancres illisibles, et la commande à coller", async ({ page, findings }) => {
  findings.where = `/machines/${noneMachine}/agent`;
  await page.goto(`/machines/${noneMachine}/agent`);
  const card = anchorsCard(page);
  await expect(card.getByRole("heading", { name: label("anchor.title") })).toBeVisible();
  // Trois types, chacun « Aucune posée » : l'ordre qu'ils bornent est refusé.
  await expect(card.locator("li[data-anchor]")).toHaveCount(3);
  await expect(card.locator('li[data-anchor="backup.guest"]')).toContainText(label("anchor.state_none"));
  await expect(card).toContainText(label("anchor.none_title"));
  await expect(card).not.toContainText(label("anchor.unreadable_title"));
  // La clé du trousseau est dans la commande : elle se colle telle quelle.
  const command = card.locator(".token-box code").first();
  await expect(command).toContainText("agent trust add backup.recipient age1");
  await card.getByRole("button", { name: label("machine.copy") }).first().click();
  await expect(card.getByRole("button", { name: new RegExp(`${label("machine.copied")}|${label("machine.copy_failed")}`) }).first()).toBeVisible();

  findings.where = `/machines/${brokenMachine}/agent`;
  await page.goto(`/machines/${brokenMachine}/agent`);
  await expect(card.locator('li[data-anchor="backup.guest"]')).toContainText(label("anchor.state_unreadable"));
  await expect(card).toContainText(label("anchor.unreadable_title"));
  await expect(card).not.toContainText(label("anchor.none_title"));
  await expect(card.locator(".token-box code").first()).toContainText("agent trust list");

  // Un agent d'avant les ancres ne les applique pas : la fiche ne prétend pas qu'il n'en a aucune.
  await page.goto("/machines");
  await page.getByRole("link", { name: /^pve$/ }).first().click();
  await page.getByRole("link", { name: label("tab.agent") }).click();
  await expect(page.locator("main")).toContainText(label("anchor.not_reported"));
  await expect(anchorsCard(page)).toHaveCount(0);
  expect(findings.unknown()).toEqual([]);
});

test("Ré-enrôler : les ancres se demandent avec le droit, la clé du trousseau est proposée, la ligne les porte", async ({ page, findings }) => {
  findings.where = `/machines/${noneMachine}/reenroller`;
  await page.goto(`/machines/${noneMachine}/reenroller`);
  const dialog = page.locator("dialog[open]");
  const produce = dialog.getByLabel(label("machine.capability_backup_produce"));
  // Le jeton d'avant ne demandait aucun droit : on le coche, et les champs apparaissent.
  await expect(dialog.locator("fieldset.anchor-fields")).toHaveCount(0);
  await produce.check();
  const fields = dialog.locator("fieldset.anchor-fields");
  await expect(fields).toContainText(label("anchor.form_title"));
  await expect(fields.getByLabel(label("anchor.label_backup_recipient"))).toHaveValue(/^age1/);
  await expect(fields.getByLabel(label("anchor.label_restic_target_cert"))).toHaveCount(0);
  await dialog.getByLabel(label("machine.capability_restic_produce")).check();
  await expect(fields.getByLabel(label("anchor.label_restic_target_cert"))).toBeVisible();

  // Une valeur qui n'a pas la forme du type est refusée par le serveur.
  await fields.getByLabel(label("anchor.label_backup_guest")).fill("0101");
  await dialog.getByRole("button", { name: label("machine.rights_submit") }).click();
  await expect(dialog).toContainText(label("machine.anchor_invalid"));

  await fields.getByLabel(label("anchor.label_backup_guest")).fill("5012, 5013");
  await fields.getByLabel(label("anchor.label_restic_target_cert")).fill("ab".repeat(32));
  await dialog.getByRole("button", { name: label("machine.rights_submit") }).click();
  const enroll = dialog.locator(".checklist-item").filter({ hasText: label("machine.install_enroll_title") });
  const line = await enroll.locator(".token-box").innerText();
  expect(line).toMatch(/ -backup-recipient age1[a-z0-9]+ -backup-guest 5012,5013 -restic-target-cert (ab){32}/);
  await dialog.getByRole("button", { name: label("wizard.next") }).click();
  await dialog.locator(".modal-foot").getByRole("button", { name: label("modal.close") }).click();
  expect(findings.unknown()).toEqual([]);
});

test("un ordre que les ancres feraient refuser n'est pas envoyé : le refus donne la commande exacte", async ({ page, findings }) => {
  findings.where = "/sauvegardes/nouvelle (VM de pve-ancres)";
  await page.goto("/sauvegardes/nouvelle");
  const dialog = page.locator("dialog[open]");
  await dialog.getByRole("radiogroup", { name: label("backup.restic_scope_label") }).getByRole("radio", { name: label("backup.restic_scope_vm") }).check();
  await dialog.getByRole("radio", { name: /^compta/ }).check();
  await dialog.getByRole("button", { name: label("wizard.next") }).click();
  await dialog.getByRole("button", { name: label("wizard.next") }).click();
  await dialog.getByRole("button", { name: label("backup.restic_submit") }).click();
  // La fenêtre reste ouverte : le serveur n'a rien envoyé, et dit quelle ancre manque.
  await expect(dialog).toContainText(label("anchor.restic_target_cert_missing"));
  await expect(dialog).toContainText(label("anchor.run_command"));
  await expect(dialog.locator(".token-box code")).toContainText(/agent trust add restic\.target-cert [0-9a-f]{64}/);
  await dialog.getByRole("button", { name: label("machine.copy") }).click();
  expect(findings.unknown()).toEqual([]);
});
