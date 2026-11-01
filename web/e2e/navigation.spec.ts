import { expect, test, type Page } from "@playwright/test";

// U2 : la navigation. Chaque tuile de la vue d'ensemble mène à sa liste, le
// menu montre tous les modules dès le premier lancement (un point signale
// ceux qui n'ont aucun objet), un renvoi de menu d'un texte est un lien qui
// arrive au bon endroit. Le serveur fictif est entièrement configuré : le
// premier lancement est celui de /api/counts réécrit en route.

const tiles = [
  { label: "Machines", path: "/machines" },
  { label: "Conteneurs", path: "/services" },
  { label: "Disponibilité", path: "/disponibilite" },
  { label: "Tâches planifiées", path: "/taches" },
  { label: "Alertes", path: "/alertes" },
];

for (const tile of tiles) {
  test(`la tuile « ${tile.label} » mène à sa liste`, async ({ page }) => {
    await page.goto("/");
    await page.locator("a.stat-link", { hasText: tile.label }).first().click();
    await expect(page).toHaveURL(new RegExp(`${tile.path}$`));
    await expect(page.locator("main h1").first()).toBeVisible();
  });
}

// Le premier lancement : aucun module n'a d'objet.
async function firstLaunch(page: Page): Promise<void> {
  await page.route("**/api/counts", async (route) => {
    const response = await route.fetch();
    const counts = (await response.json()) as { modules: { id: string; configured: boolean }[]; probes: object; jobs: object; status: object };
    counts.modules = counts.modules.map((module) => ({ ...module, configured: false }));
    counts.probes = { total: 0, attention: 0 };
    counts.jobs = { total: 0, attention: 0 };
    counts.status = { open_incidents: 0 };
    await route.fulfill({ response, json: counts });
  });
}

test("au premier lancement, le menu montre les quatre modules, chacun avec son point « À configurer »", async ({ page }) => {
  await firstLaunch(page);
  await page.goto("/");
  const side = page.locator("#side");
  for (const label of ["Pare-feu", "Sauvegardes", "Disponibilité", "Tâches planifiées"]) {
    const entry = side.locator("a.nav-item", { hasText: label });
    await expect(entry).toBeVisible();
    await expect(entry.getByRole("img", { name: "À configurer" })).toBeVisible();
  }
  // Un module du menu s'ouvre sur sa page, qui propose de le configurer.
  await side.locator("a.nav-item", { hasText: "Disponibilité" }).click();
  await expect(page).toHaveURL(/\/disponibilite$/);
  await expect(page.getByRole("link", { name: "Surveiller une cible" })).toBeVisible();
});

test("un renvoi de menu est un lien : le canal e-mail mène à Paramètres → E-mails", async ({ page }) => {
  // Aucun relais d'e-mails réglé : c'est ce qui fait dire au canal où le régler.
  await page.route("**/api/settings/mail", async (route) => {
    const response = await route.fetch();
    await route.fulfill({ response, json: { ...(await response.json()), configured: false } });
  });
  await page.goto("/parametres/canaux/nouveau");
  const dialog = page.locator("dialog[open]");
  await dialog.locator("button[role=combobox]").first().click();
  await page.locator("[role=listbox] [role=option]", { hasText: /e-?mail/i }).first().click();
  const link = dialog.getByRole("link", { name: "Paramètres → E-mails" });
  await expect(link).toBeVisible();
  await link.click();
  await expect(page).toHaveURL(/\/parametres#e-mails$/);
  await expect(page.locator("#e-mails")).toBeInViewport();
});

test("le journal s'affiche sans « interface interface »", async ({ page }) => {
  await page.goto("/journal");
  await expect(page.locator("table")).toBeVisible();
  await expect(page.locator("table")).not.toContainText("interface interface");
});
