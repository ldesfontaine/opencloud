import { expect, test, type Page } from "@playwright/test";

// Les captures de U2 (docs/projet/refonte-ux/captures/U2-*.png) : la vue
// d'ensemble aux tuiles liées, le menu du premier lancement avec ses points,
// le canal e-mail et son renvoi en lien, une liste qui défile (ombre au bord),
// l'accès SSH lu en partie, le journal et les conteneurs d'une machine.
// Données fictives seulement. Hors `make ci` : `E2E_CAPTURES=1 npm run e2e`.
test.skip(process.env.E2E_CAPTURES !== "1", "captures demandées par E2E_CAPTURES=1");

async function firstLaunch(page: Page): Promise<void> {
  await page.route("**/api/counts", async (route) => {
    const response = await route.fetch();
    const counts = await response.json();
    counts.modules = counts.modules.map((module: object) => ({ ...module, configured: false }));
    counts.probes = { total: 0, attention: 0 };
    counts.jobs = { total: 0, attention: 0 };
    counts.status = { open_incidents: 0 };
    await route.fulfill({ response, json: counts });
  });
}

async function partlyReadSSH(page: Page): Promise<void> {
  const key = { file: "authorized_keys", fingerprint: "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", type: "ssh-ed25519", bits: 256, comment: "poste-de-lucas", options: [] };
  const account = (name: string, uid: number, state: string, keys: object[]) => ({ name, uid, state, truncated: false, ignored: 0, keys });
  await page.route("**/api/ssh-keys", async (route) => {
    await route.fulfill({
      json: {
        machines: [
          {
            machine_id: "m1",
            machine_name: "vps-lyon",
            reported_at: "2026-10-28T09:00:00Z",
            truncated: false,
            accounts: [account("opencloud-agent", 998, "read", [key]), account("root", 0, "denied", [])],
          },
        ],
      },
    });
  });
}

async function mailMissing(page: Page): Promise<void> {
  await page.route("**/api/settings/mail", async (route) => {
    const response = await route.fetch();
    await route.fulfill({ response, json: { ...(await response.json()), configured: false } });
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
  { name: "vue-ensemble", url: "/", ready: ".overview-stats a.stat-link" },
  { name: "menu-premier-lancement", url: "/", ready: ".to-configure", prepare: firstLaunch },
  {
    name: "disponibilite-vide",
    url: "/disponibilite",
    ready: ".to-configure",
    prepare: firstLaunch,
  },
  {
    name: "canal-renvoi",
    url: "/parametres/canaux/nouveau",
    ready: "dialog[open]",
    prepare: mailMissing,
    open: async (page) => {
      await page.locator("dialog[open] button[role=combobox]").first().click();
      await page.locator("[role=listbox] [role=option]", { hasText: "E-mail" }).first().click();
      await expect(page.locator("dialog[open]").getByRole("link", { name: "Paramètres → E-mails" })).toBeVisible();
    },
  },
  {
    name: "liste-defilante",
    url: "/parametres/silences/nouveau",
    ready: "dialog[open]",
    open: async (page) => {
      await page.locator("dialog[open] button[role=combobox]").first().click();
      await expect(page.locator("[role=listbox]")).toBeVisible();
    },
  },
  { name: "acces-ssh", url: "/machines/acces-ssh", ready: ".verdict", prepare: partlyReadSSH },
  { name: "journal", url: "/journal", ready: "table tbody tr" },
  {
    name: "conteneurs-machine",
    url: "/machines",
    ready: "table a.svc",
    open: async (page) => {
      await page.locator("table a.svc", { hasText: "vps-paris-1" }).first().click();
      await page.getByRole("link", { name: "Conteneurs" }).first().click();
      await expect(page.locator(".image-name").first()).toBeVisible();
    },
  },
];

for (const theme of ["light", "dark"] as const) {
  for (const shot of shots) {
    test(`${shot.name} ${theme}`, async ({ page }) => {
      await page.addInitScript((value) => localStorage.setItem("theme", value), theme);
      await page.setViewportSize({ width: 1280, height: 800 });
      await shot.prepare?.(page);
      await page.goto(shot.url);
      await expect(page.locator(shot.ready).first()).toBeVisible();
      await shot.open?.(page);
      const folder = new URL("../../docs/projet/refonte-ux/captures/", import.meta.url);
      await page.screenshot({ path: new URL(`U2-${shot.name}-${theme === "light" ? "clair" : "sombre"}.png`, folder).pathname });
    });
  }
}
