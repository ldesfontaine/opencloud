import type { Page } from "@playwright/test";

import { exerciseScreen, linksOf } from "./support/actions";
import { expect, test } from "./support/findings";
import { claimShape, declaredRoutes, matchesRoute, recordedVisits, recordVisit, shapeOf } from "./support/routes";

// Le parcours : chaque écran du routeur est ouvert, chaque bouton cliqué,
// chaque fenêtre ouverte puis refermée, chaque menu déroulant ouvert avec une
// option choisie, chaque assistant mené jusqu'au récapitulatif. Zéro erreur de
// console, aucune lecture refusée.
//
// Un test par adresse sans paramètre. Les écrans à identifiant (une machine,
// un service…) se trouvent par les liens de ceux-là : le premier de chaque
// forme est parcouru dans le test qui l'a trouvé.

function trackVisits(page: Page): void {
  page.on("framenavigated", (frame) => {
    if (frame === page.mainFrame()) {
      recordVisit(new URL(frame.url()).pathname);
    }
  });
}

const staticRoutes = [...new Set(declaredRoutes().filter((route) => !route.includes(":")))];

for (const route of staticRoutes) {
  test(`écran ${route}`, async ({ page, findings }) => {
    test.setTimeout(Number(process.env.E2E_TEST_TIMEOUT ?? 8 * 60_000));
    trackVisits(page);
    const queue = [route];
    while (queue.length > 0) {
      const path = queue.shift() ?? route;
      const shape = shapeOf(path.split("?")[0] ?? path);
      if (!claimShape(shape)) {
        continue;
      }
      await exerciseScreen(page, path, findings);
      queue.push(...(await linksOf(page, path)));
    }
  });
}

// Les routes que les données fictives n'atteignent pas, et pourquoi. Une ligne
// de plus ici est une route qu'aucun test ne voit.
const notReached: Record<string, string> = {
  "/machines/:id/proxmox": "l'onglet Hyperviseur n'existe que pour une machine dont l'agent annonce le module ; aucune ne l'annonce dans les données fictives",
};

test("chaque route du routeur a été visitée", () => {
  const missing = declaredRoutes().filter((route) => !(route in notReached) && !recordedVisits().some((path) => matchesRoute(route, path)));
  expect(missing, "routes que ni un lien ni un bouton n'a menées").toEqual([]);
});
