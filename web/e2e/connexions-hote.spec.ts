import type { Page } from "@playwright/test";

import { label } from "./support/catalog";
import { expect, test } from "./support/findings";

// Le chantier L6 : les connexions de l'hôte, lues sans droit élevé. Les
// données fictives portent « dashboard », un conteneur qui publie le port 3001
// de la machine distante ; son agent a rapporté qui l'a joint : un compte de
// la machine, un portable du réseau local et Internet. Le port 9100, que
// personne ne publie, ne donne aucun lien.

const linkRows = (page: Page) => page.locator("main section", { has: page.getByText(label("link.title"), { exact: true }) }).locator("tbody tr");

async function openService(page: Page, name: string): Promise<void> {
  await page.goto("/services");
  await page.locator("main").getByRole("link", { name: new RegExp(`^${name}(\\s|$)`) }).first().click();
  await expect(page.getByRole("heading", { name, exact: true })).toBeVisible();
}

test("Fiche d'un service : les connexions vues sur son port ont une source, jamais un service", async ({ page, findings }) => {
  findings.where = "/services/annuaire/dashboard (connexions de l'hôte)";
  await openService(page, "dashboard");
  await expect(linkRows(page)).toHaveCount(3);

  const sources: Record<string, string> = {
    account: label("link.source_account").replace("%s", "backup"),
    address: label("link.source_address").replace("%s", "192.168.1.20"),
    internet: label("link.source_internet"),
  };
  for (const text of Object.values(sources)) {
    const row = linkRows(page).filter({ hasText: text });
    await expect(row).toHaveCount(1);
    await expect(row).toContainText(label("link.incoming"));
    await expect(row).toContainText(label("link.nature_traffic"));
    await expect(row).toContainText(label("link.proof_observed"));
    await expect(row).toContainText(label("link.signal_host_connection"));
    await expect(row).toContainText(label("link.port").replace("%s", "3001"));
    // Vu il y a quelques minutes : « Vu … », jamais « Calculé à l'instant » ni « Écrit le ».
    await expect(row).toContainText(label("link.seen").replace("%s", "").trim());
    // Ce n'est pas une instance : ni « Non déclaré », ni lien vers une fiche.
    await expect(row.getByText(label("link.undeclared"), { exact: true })).toHaveCount(0);
    await expect(row.getByRole("link")).toHaveCount(0);
    // Ni Confirmer, ni Écarter, ni Retirer : seulement où ça se change.
    for (const name of ["link.confirm", "link.dismiss", "link.remove"]) {
      await expect(row.getByRole("button", { name: label(name), exact: true })).toHaveCount(0);
    }
    await expect(row.locator("[title]", { hasText: "—" })).toHaveAttribute("title", label("link.from_host_help"));
    // La preuve s'explique au survol : ce que la table de l'hôte montre, et ce qu'elle ne montre pas.
    await expect(row.locator("[title]", { hasText: label("link.proof_observed") })).toHaveAttribute("title", label("link.observed_help"));
  }
  expect(findings.unknown()).toEqual([]);
});

test("Le port que personne ne publie ne donne pas de lien, et un service sans port déclaré n'en invente pas", async ({ page, findings }) => {
  findings.where = "/services/annuaire/notes (aucune connexion vue)";
  await openService(page, "notes");
  await expect(linkRows(page)).toHaveCount(1);
  await expect(linkRows(page).first()).not.toContainText(label("link.proof_observed"));
  expect(findings.unknown()).toEqual([]);
});

test("Fiche d'un service : « Ajouter un lien » reste joignable à côté des connexions vues", async ({ page, findings }) => {
  findings.where = "/services/annuaire/dashboard (ajouter un lien)";
  await openService(page, "dashboard");
  await page.getByRole("link", { name: label("link.add"), exact: true }).click();
  const dialog = page.locator("dialog[open]");
  await dialog.locator("#link-peer").click();
  // Le menu propose des services, jamais une source observée.
  const options = page.locator("[role=listbox] [role=option]");
  await expect(options.filter({ hasText: "notes" }).first()).toBeVisible();
  await expect(options.filter({ hasText: "backup" })).toHaveCount(0);
  await options.filter({ hasText: "notes" }).first().click();
  await dialog.getByRole("button", { name: label("link.dialog_save"), exact: true }).click();
  await expect(dialog).toHaveCount(0);
  await expect(linkRows(page).filter({ hasText: label("link.signal_operator") })).toHaveCount(1);
  // Retiré : le test laisse les données comme il les a trouvées.
  const written = linkRows(page).filter({ hasText: label("link.signal_operator") });
  await written.getByRole("button", { name: label("link.remove"), exact: true }).click();
  await page.getByRole("button", { name: label("confirm.yes"), exact: true }).click();
  await expect(linkRows(page)).toHaveCount(3);
  expect(findings.unknown()).toEqual([]);
});
