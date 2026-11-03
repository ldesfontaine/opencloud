import type { Page } from "@playwright/test";

import { label } from "./support/catalog";
import { expect, test } from "./support/findings";

// Les liens stockés (chantier L3). Les données fictives portent, sur la machine
// distante, l'application « nextcloud » (deux unités) et sa base « cloud-db »
// (le depends_on de Compose est leur lien d'ordre, déclaré par l'outil), un lien d'un outil
// vers un conteneur disparu et non revu depuis dix jours, et, écrit par
// l'opérateur, un lien du proxy caddy (machine openCloud) vers l'application.
// Les tests se suivent et partagent cet état ; le fichier passe avant le
// parcours générique (ordre alphabétique).
test.describe.configure({ mode: "serial" });

const options = (page: Page) => page.locator("[role=listbox] [role=option]");

async function openService(page: Page, name: string): Promise<void> {
  await page.goto("/services");
  // Le lien de la liste porte le nom puis le titre du service.
  await page.locator("main").getByRole("link", { name: new RegExp(`^${name}(\\s|$)`) }).first().click();
  await expect(page.getByRole("heading", { name, exact: true })).toBeVisible();
}

// Ferme la fenêtre d'une saisie commencée : « Annuler », puis la question confirmée.
async function abandon(page: Page): Promise<void> {
  const dialog = page.locator("dialog[open]");
  await dialog.getByRole("button", { name: label("modal.cancel"), exact: true }).click();
  await dialog.getByRole("button", { name: label("confirm.yes"), exact: true }).click();
  await expect(page.locator("dialog[open]")).toHaveCount(0);
}

const linkRows = (page: Page) => page.locator("main section", { has: page.getByText(label("link.title"), { exact: true }) }).locator("tbody tr");

test("la fiche d'un service liste ses liens, chacun avec sa preuve et sa fraîcheur", async ({ page, findings }) => {
  await openService(page, "nextcloud");
  await expect(linkRows(page)).toHaveCount(3);

  // Le depends_on de Compose : déclaré par l'outil, revu à l'instant, sans bouton pour le retirer.
  const order = linkRows(page).filter({ hasText: "cloud-db" });
  await expect(order).toContainText(label("link.outgoing"));
  await expect(order).toContainText(label("link.nature_order"));
  await expect(order).toContainText(label("link.proof_declared"));
  await expect(order).toContainText(label("link.signal_depends_on"));
  await expect(order.getByRole("link", { name: "cloud-db", exact: true })).toBeVisible();
  await expect(order.getByRole("button", { name: label("link.remove") })).toHaveCount(0);
  await expect(order.locator("[title]", { hasText: "—" })).toHaveAttribute("title", label("link.from_tool_help"));

  // Le lien d'un outil qu'on ne revoit plus : ancien, vers une instance que personne ne déclare.
  const old = linkRows(page).filter({ hasText: "legacy-cache" });
  await expect(old).toContainText(label("link.stale"));
  await expect(old).toContainText(label("link.undeclared"));
  await expect(old).toContainText(label("link.signal_container_link"));

  // Le lien de l'opérateur vient d'une autre machine : le service en face et son port se lisent.
  const mine = linkRows(page).filter({ hasText: "caddy" });
  await expect(mine).toContainText(label("link.incoming"));
  await expect(mine).toContainText(label("link.signal_operator"));
  await expect(mine).toContainText("8080");
  await expect(mine.getByRole("link", { name: "caddy", exact: true })).toBeVisible();
  expect(findings.unknown()).toEqual([]);
});

test("la base voit le même lien du côté qui l'utilise, et le conteneur non déclaré mène à sa fiche", async ({ page, findings }) => {
  await openService(page, "cloud-db");
  await expect(linkRows(page)).toHaveCount(1);
  const row = linkRows(page).first();
  await expect(row).toContainText(label("link.incoming"));
  await row.getByRole("link", { name: "nextcloud", exact: true }).click();
  await expect(page.getByRole("heading", { name: "nextcloud", exact: true })).toBeVisible();
  expect(findings.unknown()).toEqual([]);
});

test("retirer un lien écrit à la main : la question, annuler, puis confirmer", async ({ page, findings }) => {
  await openService(page, "nextcloud");
  const mine = linkRows(page).filter({ hasText: "caddy" });
  await mine.getByRole("button", { name: label("link.remove"), exact: true }).click();
  await expect(page.getByText(label("link.remove_confirm").replace("%s", "caddy"))).toBeVisible();
  await page.getByRole("button", { name: label("confirm.cancel"), exact: true }).click();
  await expect(linkRows(page)).toHaveCount(3);

  await mine.getByRole("button", { name: label("link.remove"), exact: true }).click();
  await page.getByRole("button", { name: label("confirm.yes"), exact: true }).click();
  await expect(linkRows(page)).toHaveCount(2);
  await expect(linkRows(page).filter({ hasText: "caddy" })).toHaveCount(0);
  expect(findings.unknown()).toEqual([]);
});

test("ajouter un lien de bout en bout : le sens et l'unité se choisissent dans les menus, le port se contrôle", async ({ page, findings }) => {
  await openService(page, "nextcloud");
  await page.getByRole("link", { name: label("link.add"), exact: true }).click();
  const dialog = page.locator("dialog[open]");
  await expect(dialog.getByRole("heading", { name: label("link.dialog_title") })).toBeVisible();
  const submit = dialog.getByRole("button", { name: label("link.dialog_save"), exact: true });
  await expect(submit).toBeDisabled();

  // Le sens : « Ce service est utilisé par… », choisi dans le menu.
  await dialog.locator("#link-direction").click();
  await options(page).filter({ hasText: label("link.direction_incoming") }).click();
  await expect(dialog.locator("#link-direction")).toContainText(label("link.direction_incoming"));

  await dialog.locator("#link-peer").click();
  await options(page).filter({ hasText: "caddy" }).click();

  // « nextcloud » a deux unités : le choix de la sienne apparaît, et le bouton l'attend.
  const own = dialog.locator("#link-self-unit");
  await expect(own).toBeVisible();
  await expect(submit).toBeDisabled();
  await own.click();
  await options(page).filter({ hasText: "redis" }).click();
  await expect(submit).toBeEnabled();

  // Un port faux ferme le bouton, un port juste le rouvre.
  await dialog.locator("#link-port").fill("99999");
  await expect(submit).toBeDisabled();
  await dialog.locator("#link-port").fill("6379");
  await submit.click();

  await expect(dialog).toHaveCount(0);
  const back = linkRows(page).filter({ hasText: "caddy" });
  await expect(back).toContainText(label("link.incoming"));
  await expect(back).toContainText("6379");
  expect(findings.unknown()).toEqual([]);
});

test("annuler ferme la fenêtre sans rien écrire ; le même lien deux fois est refusé avec sa phrase", async ({ page, findings }) => {
  await openService(page, "caddy");
  // Les lignes arrivent après la page : on les attend avant de les compter.
  await expect(linkRows(page)).not.toHaveCount(0);
  const before = await linkRows(page).count();
  await page.getByRole("link", { name: label("link.add"), exact: true }).click();
  let dialog = page.locator("dialog[open]");
  await dialog.locator("#link-peer").click();
  await options(page).filter({ hasText: "cloud-db" }).click();
  // La saisie est commencée : « Annuler » pose la question, on la garde d'abord, puis on abandonne.
  await dialog.getByRole("button", { name: label("modal.cancel"), exact: true }).click();
  await expect(dialog.getByText(label("modal.abandon_question"))).toBeVisible();
  await dialog.getByRole("button", { name: label("confirm.cancel"), exact: true }).click();
  await expect(dialog.locator("#link-peer")).toContainText("cloud-db");
  await abandon(page);
  await expect(linkRows(page)).toHaveCount(before);

  // caddy utilise déjà l'unité redis de nextcloud (lien posé au test d'avant) : l'écrire encore est refusé.
  await page.getByRole("link", { name: label("link.add"), exact: true }).click();
  dialog = page.locator("dialog[open]");
  await dialog.locator("#link-peer").click();
  await options(page).filter({ hasText: "nextcloud" }).first().click();
  const unit = dialog.locator("#link-peer-unit");
  await expect(unit).toBeVisible();
  await unit.click();
  await options(page).filter({ hasText: "redis" }).click();
  await dialog.getByRole("button", { name: label("link.dialog_save"), exact: true }).click();
  await expect(dialog.getByText(label("link.duplicate"))).toBeVisible();
  await abandon(page);
  expect(findings.unknown()).toEqual([]);
});

test("le réseau de la vue d'ensemble dit combien de liens il trace, et l'onglet Réseau leur preuve", async ({ page, findings }) => {
  await page.goto("/");
  await expect(page.getByText(/lien.* entre vos services/)).toBeVisible();
  await page.goto("/machines");
  await page.getByRole("link", { name: "vps-paris-1", exact: true }).first().click();
  await page.getByRole("link", { name: label("tab.network"), exact: true }).click();
  await page.locator(".netgraph-node", { hasText: "nextcloud" }).first().click();
  await expect(page.locator(".inspector")).toContainText(label("link.proof_declared"));
  expect(findings.unknown()).toEqual([]);
});

// Les liens déduits (chantier L4). Le proxy caddy fournit la capacité proxy ;
// « blog » et « wiki » ont une adresse publique : deux routes déduites. Le
// parcours en confirme une, en écarte l'autre, la rétablit, puis les laisse
// proposées.
const proposedRow = (page: Page) => linkRows(page).filter({ hasText: "caddy" });

test("une route déduite dit d'où elle vient et se propose : Confirmer, Écarter, jamais Retirer", async ({ page, findings }) => {
  await openService(page, "blog");
  await expect(linkRows(page)).toHaveCount(1);
  const row = proposedRow(page);
  await expect(row).toContainText(label("link.incoming"));
  await expect(row).toContainText(label("link.nature_route"));
  await expect(row).toContainText(label("link.proof_deduced"));
  await expect(row).toContainText(label("link.signal_public_url"));
  await expect(row).toContainText(label("link.deduced_fresh"));
  // Refait à chaque lecture : une déduction n'a pas de dernière vue, donc jamais « Ancien ».
  await expect(row).not.toContainText(label("link.stale"));
  await expect(row.locator("[title]", { hasText: label("link.proof_deduced") })).toHaveAttribute("title", label("link.deduced_help"));
  await expect(row.getByRole("button", { name: label("link.confirm"), exact: true })).toBeVisible();
  await expect(row.getByRole("button", { name: label("link.dismiss"), exact: true })).toBeVisible();
  await expect(row.getByRole("button", { name: label("link.remove") })).toHaveCount(0);
  // Le proxy voit la même route de son côté, pour les deux applications.
  await openService(page, "caddy");
  await expect(linkRows(page).filter({ hasText: "blog" })).toContainText(label("link.outgoing"));
  await expect(linkRows(page).filter({ hasText: "wiki" })).toContainText(label("link.proof_deduced"));
  expect(findings.unknown()).toEqual([]);
});

test("confirmer une route déduite la rend déclarée, et elle se retire alors comme un lien écrit à la main", async ({ page, findings }) => {
  await openService(page, "blog");
  await proposedRow(page).getByRole("button", { name: label("link.confirm"), exact: true }).click();
  const row = proposedRow(page);
  await expect(row).toContainText(label("link.proof_declared"));
  await expect(row).toContainText(label("link.signal_operator"));
  await expect(row.getByRole("button", { name: label("link.confirm"), exact: true })).toHaveCount(0);
  await expect(row.getByRole("button", { name: label("link.remove"), exact: true })).toBeVisible();

  // La carte Réseau de la vue d'ensemble trace maintenant la route confirmée.
  await page.goto("/");
  await expect(page.getByText(/lien.* entre vos services/)).toBeVisible();

  // La retirer la rend à la déduction : on la retire pour la défaire, pas pour la refuser.
  await openService(page, "blog");
  await proposedRow(page).getByRole("button", { name: label("link.remove"), exact: true }).click();
  await page.getByRole("button", { name: label("confirm.yes"), exact: true }).click();
  await expect(proposedRow(page)).toContainText(label("link.proof_deduced"));
  expect(findings.unknown()).toEqual([]);
});

test("écarter une route : elle quitte la liste, reste notée dans « Écartés », ne revient pas, et se rétablit", async ({ page, findings }) => {
  await openService(page, "wiki");
  await proposedRow(page).getByRole("button", { name: label("link.dismiss"), exact: true }).click();
  await expect(page.getByText(label("link.empty"), { exact: true })).toBeVisible();

  const fold = page.locator("summary", { hasText: label("link.dismissed_title").replace("%s", "1") });
  await expect(fold).toBeVisible();
  await fold.click();
  const kept = proposedRow(page);
  await expect(kept).toContainText(label("link.proof_deduced"));
  await expect(kept).toContainText(label("link.dismissed_on").replace("%s", ""));
  await expect(kept.getByRole("button", { name: label("link.confirm"), exact: true })).toHaveCount(0);

  // Ni un autre écran ni un rechargement ne la ramènent.
  await openService(page, "caddy");
  await expect(linkRows(page).filter({ hasText: "wiki" }).getByRole("button", { name: label("link.confirm"), exact: true })).toHaveCount(0);
  await page.reload();
  await expect(page.locator("summary", { hasText: label("link.dismissed_title").replace("%s", "1") })).toBeVisible();
  await openService(page, "wiki");
  await expect(page.getByText(label("link.empty"), { exact: true })).toBeVisible();

  // Rétablir la propose de nouveau.
  await page.locator("summary", { hasText: label("link.dismissed_title").replace("%s", "1") }).click();
  await proposedRow(page).getByRole("button", { name: label("link.restore"), exact: true }).click();
  await expect(proposedRow(page).getByRole("button", { name: label("link.confirm"), exact: true })).toBeVisible();
  await expect(page.locator("summary", { hasText: label("link.dismissed_title").replace("%s", "1") })).toHaveCount(0);
  expect(findings.unknown()).toEqual([]);
});
