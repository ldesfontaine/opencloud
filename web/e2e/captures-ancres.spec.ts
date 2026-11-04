import { expect, test, type Page } from "@playwright/test";

import { label } from "./support/catalog";

// Les captures des ancres de confiance locales (docs/projet/refonte-ux/captures/
// AL-*.png) : la fiche d'un agent sans ancre posée, puis la fenêtre
// « Ré-enrôler » ouverte avec ses champs d'ancres et la clé du trousseau
// proposée. Données fictives seulement. Hors `make ci` : `E2E_CAPTURES=1 npm run e2e`.
test.skip(process.env.E2E_CAPTURES !== "1", "captures demandées par E2E_CAPTURES=1");

const folder = new URL("../../docs/projet/refonte-ux/captures/", import.meta.url);
const noneMachine = "aaaaaaaa-1111-4222-8333-444444444444";

async function open(page: Page, path: string): Promise<void> {
  await page.setViewportSize({ width: 1500, height: 900 });
  await page.clock.setFixedTime(new Date("2026-09-12T12:00:00Z"));
  await page.addInitScript(() => localStorage.setItem("theme", "light"));
  await page.goto(path);
}

async function shoot(page: Page, name: string): Promise<void> {
  await page.waitForTimeout(250);
  await page.screenshot({ path: new URL(`AL-${name}-clair.png`, folder).pathname });
}

test("la fiche : aucune ancre posée, la commande à coller", async ({ page }) => {
  await open(page, `/machines/${noneMachine}/agent`);
  const card = page.locator("section.agent-anchors");
  await expect(card).toContainText(label("anchor.none_title"));
  await card.scrollIntoViewIfNeeded();
  await shoot(page, "fiche");
});

test("la fenêtre Ré-enrôler ouverte : les ancres demandées avec le droit", async ({ page }) => {
  await open(page, `/machines/${noneMachine}/reenroller`);
  const dialog = page.locator("dialog[open]");
  await dialog.getByLabel(label("machine.capability_backup_produce")).check();
  await dialog.getByLabel(label("machine.capability_restic_produce")).check();
  await dialog.getByLabel(label("anchor.label_backup_guest")).fill("5012, 5013");
  await expect(dialog.getByLabel(label("anchor.label_backup_recipient"))).toHaveValue(/^age1/);
  await shoot(page, "reenroler");
});
