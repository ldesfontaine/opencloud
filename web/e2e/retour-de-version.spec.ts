import { mkdirSync } from "node:fs";

import type { Locator, Page } from "@playwright/test";

import { label } from "./support/catalog";
import { expect, test, type Findings } from "./support/findings";

// Le retour de version automatique d'un service (lot A), sur les données
// fictives : agora (web-1) a tenté 1.5, qui n'a pas tenu, et la machine est
// revenue seule à 1.4 ; lexique (web-1) n'a jamais changé de version ;
// gazette (web-ancien) a un agent sans filet. Chaque écran touché est cliqué :
// la carte Mise à jour d'un conteneur (« Passer à » et sa confirmation), la
// carte Déployer du service, la boîte d'alertes (le journal joint, replié puis
// ouvert), l'assistant de canal (« Joindre les journaux »). Avec
// E2E_CAPTURES=1, il laisse les captures RV-*.

const captureFolder = new URL("../../docs/projet/refonte-ux/captures/", import.meta.url).pathname;
const capturing = process.env.E2E_CAPTURES === "1";

async function shoot(page: Page, name: string): Promise<void> {
  if (!capturing) {
    return;
  }
  mkdirSync(captureFolder, { recursive: true });
  await page.waitForTimeout(250);
  await page.screenshot({ path: `${captureFolder}RV-${name}-clair.png` });
}

// Les morceaux fixes d'une phrase du catalogue, autour de ses %s.
function pieces(key: string): string[] {
  return label(key)
    .split("%s")
    .map((piece) => piece.trim())
    .filter((piece) => piece !== "");
}

function filled(key: string, ...args: string[]): string {
  return args.reduce((text, arg) => text.replace("%s", arg), label(key));
}

async function containerID(page: Page, name: string): Promise<string> {
  const answer = (await (await page.request.get("/api/containers")).json()) as { containers: { id: string; name: string }[] };
  const found = answer.containers.find((candidate) => candidate.name === name);
  if (found === undefined) {
    throw new Error(`le conteneur ${name} des données fictives manque`);
  }
  return found.id;
}

async function serviceID(page: Page, name: string): Promise<string> {
  const answer = (await (await page.request.get("/api/directory/services")).json()) as { services: { id: string; name: string }[] };
  const found = answer.services.find((candidate) => candidate.name === name);
  if (found === undefined) {
    throw new Error(`le service ${name} des données fictives manque`);
  }
  return found.id;
}

async function openUpdateCard(page: Page, findings: Findings, name: string): Promise<Locator> {
  const path = `/services/conteneurs/${await containerID(page, name)}`;
  findings.where = path;
  await page.goto(path);
  const card = page.locator(".card").filter({ has: page.getByText(label("update.title"), { exact: true }) });
  await expect(card).toBeVisible();
  return card;
}

function upgradeButton(card: Locator, version: string): Locator {
  return card.getByRole("button", { name: filled("update.upgrade_to", version), exact: true });
}

test.beforeEach(async ({ page }) => {
  await page.setViewportSize({ width: 1500, height: 1000 });
  await page.addInitScript(() => localStorage.setItem("theme", "light"));
});

test("agora : la montée revenue en arrière se dit, et « Passer à 1.5 » ouvre sa confirmation avec le filet", async ({ page, findings }) => {
  const card = await openUpdateCard(page, findings, "agora-app-1");
  const note = card.locator(".service-update-note");
  await expect(note).toContainText(filled("update.failed_title", "1.5"));
  await expect(note).toContainText(`${pieces("update.failed_rolled_back")[1] ?? ""} 1.4`);
  await expect(note.locator(".pill-warn")).toBeVisible();

  const button = upgradeButton(card, "1.5");
  await expect(button).toBeEnabled();
  await button.click();
  const confirm = card.getByRole("group", { name: filled("update.upgrade_to", "1.5") });
  await expect(confirm).toBeVisible();
  // La confirmation dit le filet de 5 minutes, et que les données ne reviennent pas.
  for (const piece of pieces("update.upgrade_confirm").slice(-1)) {
    await expect(confirm).toContainText(piece);
  }
  await card.scrollIntoViewIfNeeded();
  await shoot(page, "confirmation");
  await confirm.getByRole("button", { name: label("update.upgrade_cancel") }).click();
  await expect(confirm).toHaveCount(0);
  await expect(upgradeButton(card, "1.5")).toBeVisible();

  // La carte Déployer du service dit la même montée, sous le dernier ordre.
  const path = `/services/annuaire/${await serviceID(page, "agora")}`;
  findings.where = path;
  await page.goto(path);
  const deploy = page.locator(".card").filter({ has: page.getByText(label("deploy.title"), { exact: true }) });
  await expect(deploy.locator(".service-update-note")).toContainText(filled("update.failed_title", "1.5"));
  expect(findings.unknown()).toEqual([]);
});

test("gazette : un agent sans filet garde « Passer à » visible, grisé, avec sa raison", async ({ page, findings }) => {
  const card = await openUpdateCard(page, findings, "gazette-app-1");
  await expect(upgradeButton(card, "1.1")).toBeDisabled();
  await expect(card).toContainText(label("update.upgrade_rollback_unavailable"));
  await expect(card.locator(".service-update-note")).toHaveCount(0);
  expect(findings.unknown()).toEqual([]);
});

test("lexique : l'état vide (aucune montée), puis l'erreur de lecture du plan", async ({ page, findings }) => {
  // Vide : jamais de montée, aucun bloc ; le geste est possible.
  const card = await openUpdateCard(page, findings, "lexique-app-1");
  await expect(upgradeButton(card, "2.1")).toBeEnabled();
  await expect(card.locator(".service-update-note")).toHaveCount(0);
  await expect(card).not.toContainText(label("update.upgrade_rollback_unavailable"));

  // Erreur de lecture : la carte dit l'erreur, sans bouton ni bloc trompeur.
  await page.route("**/api/directory/services/*/deploy-plan", (route) => route.fulfill({ status: 500, json: { error: "internal" } }));
  const failing = await openUpdateCard(page, findings, "lexique-app-1");
  await expect(failing).toContainText(label("error.internal"));
  await expect(upgradeButton(failing, "2.1")).toHaveCount(0);
  await expect(failing.locator(".service-update-note")).toHaveCount(0);
  findings.all.splice(0, findings.all.length, ...findings.all.filter((finding) => !/deploy-plan répond 500/.test(finding.what)));
  expect(findings.unknown()).toEqual([]);
});

test("la boîte d'alertes : la montée revenue en arrière, son journal replié puis ouvert", async ({ page, findings }) => {
  findings.where = "/alertes";
  await page.goto("/alertes");
  const row = page.locator("main .alert-row").filter({ hasText: label("alert.fact_service_rolled_back") }).first();
  await expect(row).toBeVisible();
  await expect(row).toContainText(filled("alert.cause_service_rolled_back", "agora", "web-1", "1.5", "1.4"));
  // L'objet mène à la fiche du service ; le silence vise le service.
  await expect(row.getByRole("link", { name: "agora", exact: true })).toHaveAttribute("href", /^\/services\/annuaire\/svc_/);
  await expect(row.getByRole("link", { name: label("alerts.silence") })).toHaveAttribute("href", /target_kind=service/);

  const log = row.locator("details.fold").filter({ hasText: label("alert.logs_label") });
  await expect(log).not.toHaveAttribute("open");
  await expect(log.locator("pre")).toBeHidden();
  await log.locator("summary").click();
  await expect(log.locator("pre")).toBeVisible();
  await expect(log.locator("pre")).toContainText('column "slug" already exists');
  await row.scrollIntoViewIfNeeded();
  await shoot(page, "alerte");
  expect(findings.unknown()).toEqual([]);
});

test("l'assistant de canal : « Joindre les journaux » cochée par défaut, dite dans le résumé", async ({ page, findings }) => {
  findings.where = "/parametres/canaux/nouveau";
  await page.goto("/parametres/canaux/nouveau");
  const dialog = page.locator("dialog[open]");
  await expect(dialog).toBeVisible();
  const fold = dialog.locator("details.fold").filter({ hasText: label("wizard.settings") });
  await expect(fold.locator("summary")).toContainText(label("channel.hint_logs"));
  await fold.locator("summary").click();
  const box = fold.getByRole("checkbox", { name: label("channel.attach_logs_label") });
  await expect(box).toBeChecked();
  await expect(fold).toContainText(label("channel.attach_logs_help"));
  await box.uncheck();
  await expect(fold.locator("summary")).not.toContainText(label("channel.hint_logs"));
  await box.check();
  await expect(fold.locator("summary")).toContainText(label("channel.hint_logs"));
  expect(findings.unknown()).toEqual([]);
});
