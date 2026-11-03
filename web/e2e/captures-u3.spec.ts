import { expect, test, type Page } from "@playwright/test";

import { label } from "./support/catalog";
import { createFirstAccount, mockFirstSetup } from "./support/firstSetup";

// Les captures de U3 (docs/projet/refonte-ux/captures/U3-*.png) : les
// assistants d'un seul écran (sonde, tâche, canal, silence, projet, plan),
// leur pli « Réglages » ouvert, un menu ouvert dans une fenêtre, et la note de
// l'adresse locale, neutre, puis rouge quand la page est publique. Données
// fictives seulement. Hors `make ci` : `E2E_CAPTURES=1 npm run e2e`.
test.skip(process.env.E2E_CAPTURES !== "1", "captures demandées par E2E_CAPTURES=1");

async function openSettings(page: Page): Promise<void> {
  await page.locator("dialog[open] details.fold > summary").first().click();
  await expect(page.locator("dialog[open] details.fold[open]").first()).toBeVisible();
}

// Une tâche créée pour la fiche : l'adresse du banc est locale.
async function withPublicPage(page: Page): Promise<void> {
  await page.route("**/api/jobs/*", async (route) => {
    if (route.request().method() !== "GET") {
      await route.fallback();
      return;
    }
    const response = await route.fetch();
    const body = await response.json();
    await route.fulfill({ response, json: { ...body, url_local: true, url_suggestion: "https://cloud.exemple.fr" } });
  });
}

interface Shot {
  name: string;
  url: string;
  ready: string;
  prepare?: (page: Page) => Promise<void>;
  open?: (page: Page) => Promise<void>;
}

const shots: Shot[] = [
  { name: "sonde", url: "/disponibilite/nouvelle", ready: "dialog[open] #probe-name" },
  {
    name: "sonde-reglages",
    url: "/disponibilite/nouvelle",
    ready: "dialog[open] #probe-name",
    open: async (page) => {
      await page.locator("#probe-name").fill("site nextcloud");
      await page.locator("#probe-target").fill("https://cloud.exemple.fr/");
      await openSettings(page);
    },
  },
  {
    name: "sonde-menu",
    url: "/disponibilite/nouvelle",
    ready: "dialog[open] #probe-name",
    open: async (page) => {
      await openSettings(page);
      await page.locator("#probe-machine").click();
      await expect(page.locator("[role=listbox]")).toBeVisible();
    },
  },
  { name: "tache", url: "/taches/nouvelle", ready: "dialog[open] #job-name" },
  { name: "canal", url: "/parametres/canaux/nouveau", ready: "dialog[open] #channel-name" },
  {
    name: "canal-reglages",
    url: "/parametres/canaux/nouveau",
    ready: "dialog[open] #channel-name",
    open: async (page) => {
      await page.locator("#channel-name").fill("salon ops");
      await page.locator("#channel-url").fill("https://hooks.exemple.fr/oc");
      await openSettings(page);
    },
  },
  { name: "silence", url: "/parametres/silences/nouveau", ready: "dialog[open] #silence-target-kind" },
  {
    name: "projet",
    url: "/services/projets/nouveau",
    ready: "dialog[open] #project-name",
    open: async (page) => {
      await page.locator("#project-name").fill("client-a");
      await page.getByLabel(label("project.publish_check")).check();
    },
  },
  { name: "plan", url: "/sauvegardes/plans/nouveau", ready: "dialog[open] #backup-plan-kind" },
  {
    name: "note-locale",
    url: "/taches/nouvelle",
    ready: "dialog[open] #job-name",
    open: async (page) => {
      await page.locator("#job-name").fill("sauvegarde nextcloud");
      await page.getByRole("button", { name: label("job.create"), exact: true }).click();
      await expect(page.locator(".note-neutral")).toBeVisible();
    },
  },
  {
    name: "note-rouge",
    url: "/taches/nouvelle",
    ready: "dialog[open] #job-name",
    prepare: withPublicPage,
    open: async (page) => {
      await page.locator("#job-name").fill("sauvegarde nextcloud");
      await page.getByRole("button", { name: label("job.create"), exact: true }).click();
      await expect(page.locator(".note-danger")).toBeVisible();
    },
  },
  {
    name: "premiere-configuration",
    url: "/",
    ready: ".note-neutral",
    prepare: (page) => mockFirstSetup(page, { local: true, suggestion: "" }),
    open: async () => undefined,
  },
];

for (const theme of ["light", "dark"] as const) {
  for (const shot of shots) {
    test(`${shot.name} ${theme}`, async ({ page }) => {
      await page.addInitScript((value) => localStorage.setItem("theme", value), theme);
      await page.setViewportSize({ width: 1280, height: 800 });
      await shot.prepare?.(page);
      await page.goto(shot.url);
      if (shot.name === "premiere-configuration") {
        await createFirstAccount(page);
      }
      await expect(page.locator(shot.ready).first()).toBeVisible();
      await shot.open?.(page);
      const folder = new URL("../../docs/projet/refonte-ux/captures/", import.meta.url);
      await page.screenshot({ path: new URL(`U3-${shot.name}-${theme === "light" ? "clair" : "sombre"}.png`, folder).pathname });
    });
  }
}
