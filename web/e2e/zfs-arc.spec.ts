import { label } from "./support/catalog";
import { expect, test } from "./support/findings";

// La tuile mémoire d'une fiche de machine et le cache ZFS, sur les données
// fictives : pve-ancres est un Pi en ZFS (1,2 Go utilisés, 9,2 Gio d'ARC à
// part), la machine openCloud n'a pas ZFS (ni ligne de cache), pve-illisible
// n'a aucune mesure (vide), et la lecture qui échoue est un autre état.

const zfsMachine = "aaaaaaaa-1111-4222-8333-444444444444";
const emptyMachine = "bbbbbbbb-1111-4222-8333-555555555555";

function memoryTile(page: import("@playwright/test").Page) {
  return page.locator(".resource-stats .stat").filter({ hasText: label("resource.memory") });
}

test("la tuile mémoire montre le cache ZFS à part, la valeur utilisée est la corrigée", async ({ page, findings }) => {
  findings.where = `/machines/${zfsMachine}`;
  await page.goto(`/machines/${zfsMachine}`);
  const tile = memoryTile(page);
  await expect(tile).toContainText(/1[.,]2/);
  await expect(tile).toContainText(/9[.,]2/);
  await expect(tile).toContainText(/ZFS/);
  // L'ARC n'est pas dans la jauge : 1,2 Go sur 15 Go, pas 11.
  await expect(tile).toContainText(/8 %/);
  expect(findings.unknown()).toEqual([]);
});

test("une machine sans ZFS ne montre aucune ligne de cache", async ({ page, findings }) => {
  findings.where = "/machines/local";
  await page.goto("/machines/local");
  const tile = memoryTile(page);
  await expect(tile).toContainText(/%/);
  await expect(tile).not.toContainText(/ZFS/);
  expect(findings.unknown()).toEqual([]);
});

test("sans mesure : le vide dit « aucune mesure », pas une erreur de lecture", async ({ page, findings }) => {
  findings.where = `/machines/${emptyMachine}`;
  await page.goto(`/machines/${emptyMachine}`);
  const tile = memoryTile(page);
  await expect(tile).toContainText(label("resource.unavailable_text"));
  await expect(tile).not.toContainText(label("resource.read_failed"));
  expect(findings.unknown()).toEqual([]);
});

test("lecture en erreur : la tuile le dit, autrement que le vide", async ({ page, findings }) => {
  await page.route(`**/api/machines/${zfsMachine}/resources`, (route) => route.fulfill({ status: 500, contentType: "application/json", body: "{}" }));
  findings.where = `/machines/${zfsMachine} (lecture en erreur)`;
  await page.goto(`/machines/${zfsMachine}`);
  const tile = memoryTile(page);
  await expect(tile).toContainText(label("resource.read_failed"));
  await expect(tile).not.toContainText(label("resource.unavailable_text"));
  // Le 500 est voulu : le parcours l'a provoqué.
  findings.all.length = 0;
  expect(findings.unknown()).toEqual([]);
});

test("la fenêtre Ré-enrôler s'ouvre par-dessus la fiche, la tuile mémoire reste lisible", async ({ page, findings }) => {
  findings.where = `/machines/${zfsMachine}`;
  await page.goto(`/machines/${zfsMachine}`);
  await page.getByRole("link", { name: label("machine.reenroll") }).first().click();
  await expect(page.locator("dialog[open]")).toBeVisible();
  await expect(memoryTile(page)).toContainText(/ZFS/);
  expect(findings.unknown()).toEqual([]);
});
