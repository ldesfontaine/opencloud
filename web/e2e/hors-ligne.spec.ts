import { type Locator, type Page } from "@playwright/test";

import { label } from "./support/catalog";
import { expect, test } from "./support/findings";

// La machine distante des données fictives n'a pas d'agent connecté : elle est
// hors ligne. Chaque geste qui exige l'agent y est désactivé, avec la raison
// affichée à côté, jamais seulement caché ou refusé par le serveur.
const reason = label("agent.offline_reason");

async function remoteID(page: Page): Promise<string> {
  const response = await page.request.get("/api/machines");
  const body = (await response.json()) as { machines: { id: string; name: string; online: boolean }[] };
  // Les données fictives ont d'autres machines, en ligne (les sauvegardes
  // restic) : la distante est celle qui porte ce nom.
  const remote = body.machines.find((machine) => machine.name === "vps-paris-1");
  expect(remote?.online, "la machine distante des données fictives est hors ligne").toBe(false);
  return remote?.id ?? "";
}

async function expectGated(gate: Locator): Promise<void> {
  await expect(gate.locator(".agent-gate-reason")).toHaveText(reason);
  const buttons = gate.locator("button");
  expect(await buttons.count()).toBeGreaterThan(0);
  for (let index = 0; index < (await buttons.count()); index++) {
    await expect(buttons.nth(index)).toBeDisabled();
  }
}

test("fiche machine : ping et mise à jour de l'agent", async ({ page }) => {
  await page.goto(`/machines/${await remoteID(page)}/agent`);
  const gates = page.locator("main .agent-gate");
  await expect(gates.first()).toBeVisible();
  for (let index = 0; index < (await gates.count()); index++) {
    await expectGated(gates.nth(index));
  }
});

test("onglet Journaux : « Voir les journaux » est grisé, sans lien", async ({ page }) => {
  await page.goto(`/machines/${await remoteID(page)}/journaux`);
  const gates = page.locator("main .agent-gate");
  await expect(gates.first()).toBeVisible();
  for (let index = 0; index < (await gates.count()); index++) {
    await expectGated(gates.nth(index));
  }
  await expect(page.locator("main a[href*='#journaux']")).toHaveCount(0);
});

test("fiche conteneur : journaux et vérification disent la raison", async ({ page, findings }) => {
  await page.goto(`/machines/${await remoteID(page)}/services`);
  const link = page.locator("a[href^='/services/conteneurs/']").first();
  await expect(link).toBeVisible();
  await link.click();
  await expect(page.locator("#journaux")).toContainText(reason);
  await expectGated(page.locator("main .update-actions .agent-gate").last());
  // Aucune requête de journaux ne part vers une machine qui n'y répondra pas.
  expect(findings.all.filter((finding) => /logs/.test(finding.what))).toEqual([]);
});

test("sauvegardes : « lancer maintenant » est grisé quand la destination est hors ligne", async ({ page }) => {
  await page.goto("/sauvegardes");
  // Les plans d'archives sont repliés sous leur section, la base étant sauvegardée.
  await page.locator("#archives details > summary").click();
  const gate = page.locator("main .agent-gate").first();
  await expect(gate).toBeVisible();
  await expectGated(gate);
});

test("service déclaré : déployer est grisé", async ({ page }) => {
  await page.goto("/services");
  const service = page.locator("a[href^='/services/annuaire/svc_']", { hasText: /nextcloud/i }).first();
  await service.click();
  const gate = page.locator("main .agent-gate").first();
  await expect(gate).toBeVisible();
  await expectGated(gate);
});
