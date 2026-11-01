import { label } from "./support/catalog";
import { expect, test } from "./support/findings";

// Un service par service Compose, et « Déclarer un service » qui part des
// conteneurs détectés (chantier L1). Les données fictives portent le projet
// Compose « edge » : traefik déclaré sur deux machines (une ligne, deux
// instances), authelia et crowdsec détectés sur la machine distante sans être
// déclarés. Les tests se suivent et partagent cet état ; le fichier passe avant
// le parcours générique (ordre alphabétique) qui accepterait ces propositions
// en cliquant chaque bouton.
test.describe.configure({ mode: "serial" });

test("la liste range chaque service sous son projet Compose, et plie le même service de deux machines", async ({ page, findings }) => {
  await page.goto("/services");
  const table = page.locator("main table:has(tr.svc-project)");
  await expect(table.locator("thead th", { hasText: label("directory.col_category") })).toBeVisible();
  const project = table.locator("tr.svc-project", { hasText: "edge" });
  await expect(project).toBeVisible();

  // traefik tourne sur deux machines : une ligne, qui se déplie en une ligne par machine.
  const toggle = table.getByRole("button", { name: "Déplier les machines de traefik" });
  await expect(toggle).toContainText("2 machines");
  await expect(table.locator("tr.svc-instance")).toHaveCount(0);
  await toggle.click();
  await expect(table.locator("tr.svc-instance")).toHaveCount(2);
  await expect(table.getByRole("button", { name: "Replier les machines de traefik" })).toBeVisible();
  // Chaque ligne dit ce qu'est le service : traefik est un serveur web.
  await expect(table.locator("tr.svc-group").getByText(label("directory.category_web"))).toBeVisible();
  await table.getByRole("button", { name: "Replier les machines de traefik" }).click();
  await expect(table.locator("tr.svc-instance")).toHaveCount(0);
  expect(findings.unknown()).toEqual([]);
});

test("les propositions sont des services Compose, rangés sous leur projet", async ({ page, findings }) => {
  await page.goto("/services");
  const proposals = page.locator("main table:not(:has(tr.svc-project))").first();
  await expect(proposals.getByText("authelia", { exact: true })).toBeVisible();
  await expect(proposals.getByText("crowdsec", { exact: true })).toBeVisible();
  await expect(proposals.getByText("Projet Compose edge").first()).toBeVisible();
  expect(findings.unknown()).toEqual([]);
});

test("déclarer un service : un choix dans la liste des conteneurs détectés remplit tout le reste", async ({ page, findings }) => {
  await page.goto("/services");
  await page.getByRole("link", { name: label("directory.add"), exact: true }).click();
  const dialog = page.locator("dialog[open]");
  await expect(dialog.getByText(label("directory.pick_help"))).toBeVisible();

  await dialog.locator("#service-pick").click();
  const options = page.locator("[role=listbox] [role=option]");
  await expect(options.filter({ hasText: "authelia (edge)" })).toBeVisible();
  await expect(options.filter({ hasText: "crowdsec (edge)" })).toBeVisible();
  await options.filter({ hasText: "authelia (edge)" }).click();

  // Rien à retaper : le nom, l'image et le projet Compose viennent du conteneur.
  await expect(dialog.locator("#service-name")).toHaveValue("authelia");
  await expect(dialog.locator("#service-compose")).toHaveValue("edge");
  await dialog.getByRole("button", { name: label("wizard.next"), exact: true }).click();
  await expect(dialog.locator("#unit-0-image")).toHaveValue("authelia/authelia");
  await dialog.getByRole("button", { name: label("wizard.next"), exact: true }).click();
  // Les projets sont facultatifs : on passe au récapitulatif sans en choisir.
  await expect(dialog.getByText(label("directory.projects_optional"))).toBeVisible();
  await dialog.getByRole("button", { name: label("wizard.next"), exact: true }).click();
  await expect(dialog.locator(".wizard-summary")).toBeVisible();
  await dialog.getByRole("button", { name: label("directory.create"), exact: true }).click();

  // La fiche dit ce qu'est le service, et que c'est déduit de l'image.
  await expect(page).toHaveURL(/\/services\/annuaire\/svc_/);
  await expect(page.locator("main")).toContainText(label("directory.category_sso"));
  await expect(page.locator("main")).toContainText(label("directory.category_deduced_note"));
  expect(findings.unknown()).toEqual([]);
});

test("le type se corrige à la main, sans plus suivre l'image", async ({ page, findings }) => {
  await page.goto("/services");
  const row = page.locator("main table tr", { hasText: "authelia" }).first();
  await row.getByRole("link", { name: "authelia" }).click();
  await page.getByRole("button", { name: label("directory.edit") }).or(page.getByRole("link", { name: label("directory.edit") })).first().click();
  const dialog = page.locator("dialog[open]");
  // L'édition s'ouvre sur le récapitulatif : on rouvre l'étape Identité.
  await dialog.getByRole("button", { name: label("wizard.edit_step").replace("%s", label("directory.step_identity")) }).click();
  await dialog.locator("#service-category").click();
  await page.locator("[role=listbox] [role=option]", { hasText: label("directory.category_security") }).click();
  await dialog.getByRole("button", { name: label("wizard.back_to_summary"), exact: true }).click();
  await dialog.getByRole("button", { name: label("directory.save"), exact: true }).click();
  await expect(page.locator("main")).toContainText(label("directory.category_security"));
  await expect(page.locator("main")).not.toContainText(label("directory.category_deduced_note"));
  expect(findings.unknown()).toEqual([]);
});

test("accepter une proposition déclare ce seul service Compose, à côté de ses voisins du projet", async ({ page, findings }) => {
  await page.goto("/services");
  const proposals = page.locator("main table:not(:has(tr.svc-project))").first();
  await proposals.locator("tr", { hasText: "crowdsec" }).getByRole("button", { name: label("gap.accept"), exact: true }).click();
  const table = page.locator("main table:has(tr.svc-project)");
  const project = table.locator("tr.svc-project", { hasText: "edge" });
  await expect(project).toBeVisible();
  await expect(table.locator("tr", { hasText: "crowdsec" })).toContainText(label("directory.category_security"));
  await expect(table.locator("tr", { hasText: "authelia" })).toBeVisible();
  // Le projet ne s'est pas déclaré d'un bloc : il ne reste plus que lui à proposer.
  await expect(proposals.locator("tr", { hasText: "crowdsec" })).toHaveCount(0);
  expect(findings.unknown()).toEqual([]);
});

test("saisir à la main : l'étape Projets crée un projet sans quitter l'assistant", async ({ page, findings }) => {
  await page.goto("/services/annuaire/nouveau");
  const dialog = page.locator("dialog[open]");
  await dialog.getByRole("button", { name: label("directory.pick_manual"), exact: true }).click();
  await expect(dialog.locator("#service-name")).toHaveValue("");
  await dialog.locator("#service-name").fill("mon-service");
  await dialog.locator("#service-compose").fill("e2e");
  await dialog.locator("#service-category").click();
  await page.locator("[role=listbox] [role=option]", { hasText: label("directory.category_monitoring") }).click();
  await dialog.getByRole("button", { name: label("wizard.next"), exact: true }).click();
  await dialog.locator("#unit-0-name").fill("web");
  await dialog.locator("#unit-0-image").fill("nginx");
  await dialog.getByRole("button", { name: label("wizard.next"), exact: true }).click();

  await dialog.locator("#service-new-project").fill("e2e-projet");
  await dialog.getByRole("button", { name: label("directory.projects_new_create"), exact: true }).click();
  await expect(dialog.getByRole("checkbox", { name: "e2e-projet" })).toBeChecked();
  await dialog.getByRole("button", { name: label("wizard.next"), exact: true }).click();
  await expect(dialog.locator(".wizard-summary")).toContainText("e2e-projet");
  await expect(dialog.locator(".wizard-summary")).toContainText(label("directory.category_monitoring"));
  await dialog.getByRole("button", { name: label("modal.cancel"), exact: true }).first().click();
  await dialog.locator(".modal-foot button").first().click();
  await expect(dialog).toHaveCount(0);
  expect(findings.unknown()).toEqual([]);
});
