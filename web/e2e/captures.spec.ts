import { expect, test } from "@playwright/test";

// Les captures de la documentation (docs/projet/refonte-ux/captures/F1-*.png) :
// la liste d'un menu déroulant ouverte dans une fenêtre, en clair et en sombre.
// Données fictives seulement. Hors `make ci` : `E2E_CAPTURES=1 npm run e2e`.
test.skip(process.env.E2E_CAPTURES !== "1", "captures demandées par E2E_CAPTURES=1");

const shots = [
  { name: "sonde-type", url: "/disponibilite/nouvelle", trigger: "#probe-kind" },
  { name: "plan-source", url: "/sauvegardes/plans/nouveau", trigger: "#backup-plan-kind" },
  { name: "canal-format", url: "/parametres/canaux/nouveau", trigger: "button[role=combobox]" },
];

for (const theme of ["light", "dark"] as const) {
  for (const shot of shots) {
    test(`${shot.name} ${theme}`, async ({ page }) => {
      await page.addInitScript((value) => localStorage.setItem("theme", value), theme);
      await page.goto(shot.url);
      const dialog = page.locator("dialog[open]");
      await expect(dialog).toBeVisible();
      if (shot.name === "sonde-type") {
        await dialog.locator("details.fold > summary").first().click();
      }
      await dialog.locator(shot.trigger).first().click();
      await expect(dialog.locator("[role=listbox]")).toBeVisible();
      const folder = new URL("../../docs/projet/refonte-ux/captures/", import.meta.url);
      await page.screenshot({ path: new URL(`F1-${shot.name}-${theme === "light" ? "clair" : "sombre"}.png`, folder).pathname });
    });
  }
}
