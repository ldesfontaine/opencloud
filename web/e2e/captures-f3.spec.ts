import { expect, test } from "@playwright/test";

// Les captures de F3 (docs/projet/refonte-ux/captures/F3-*.png) : la barre
// latérale et les cartes de la vue d'ensemble, la liste des machines, la fiche
// d'une machine et un menu de machine, avec le type partout. Données fictives
// seulement. Hors `make ci` : `E2E_CAPTURES=1 npm run e2e`.
test.skip(process.env.E2E_CAPTURES !== "1", "captures demandées par E2E_CAPTURES=1");

const shots = [
  { name: "vue-ensemble", url: "/", ready: ".machine-card .machine-kind" },
  { name: "machines", url: "/machines", ready: "table .machine-kind" },
  { name: "fiche-machine", url: "/machines", ready: "table .machine-kind", open: "table a.svc" },
  { name: "menu-machine", url: "/disponibilite/nouvelle", ready: "dialog[open]", trigger: "#probe-machine" },
];

for (const theme of ["light", "dark"] as const) {
  for (const shot of shots) {
    test(`${shot.name} ${theme}`, async ({ page }) => {
      await page.addInitScript((value) => localStorage.setItem("theme", value), theme);
      await page.setViewportSize({ width: 1280, height: 800 });
      await page.goto(shot.url);
      await expect(page.locator(shot.ready).first()).toBeVisible();
      if (shot.open !== undefined) {
        await page.locator(shot.open).first().click();
        await expect(page.locator(".machine-columns .machine-kind").first()).toBeVisible();
      }
      if (shot.trigger !== undefined) {
        await page.locator(shot.trigger).click();
        await expect(page.locator("[role=listbox]")).toBeVisible();
      }
      const folder = new URL("../../docs/projet/refonte-ux/captures/", import.meta.url);
      await page.screenshot({ path: new URL(`F3-${shot.name}-${theme === "light" ? "clair" : "sombre"}.png`, folder).pathname });
    });
  }
}
