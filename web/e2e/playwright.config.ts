import { createHash } from "node:crypto";

import { defineConfig } from "@playwright/test";

// Un port par copie du dépôt : plusieurs copies de travail (une par chantier)
// lancent le parcours sur la même machine, et un port commun les faisait
// s'arrêter les unes les autres (le serveur de l'une recevait l'arrêt de l'autre,
// le parcours finissait en ECONNREFUSED). Le chemin de la copie fixe le port,
// entre 20000 et 31999, sous les ports éphémères de Linux ; deux copies ne le partagent qu'en cas de collision de
// hachage, et Playwright refuse alors de démarrer au lieu de tuer l'autre.
function portOfCheckout(): number {
  const digest = createHash("sha256").update(process.cwd()).digest();
  return 20000 + (digest.readUInt16BE(0) % 12000);
}

// Le serveur de données fictives est le test Go TestE2EFixtureServer, compilé
// par `make e2e` en .fixture.test : le vrai serveur, sa vraie base, l'horloge
// figée, rien de réel.
// OPENCLOUD_E2E_ADDR impose l'adresse.
const address = process.env.OPENCLOUD_E2E_ADDR ?? `127.0.0.1:${portOfCheckout()}`;

export default defineConfig({
  testDir: ".",
  testMatch: "*.spec.ts",
  outputDir: ".results",
  // Un seul serveur, un seul état : les écrans se parcourent à la suite.
  workers: 1,
  fullyParallel: false,
  timeout: 10 * 60_000,
  reporter: [["list"]],
  use: {
    baseURL: `http://${address}`,
    viewport: { width: 1280, height: 900 },
    locale: "fr-FR",
    colorScheme: "light",
    // Sans limite, un élément absent bloquerait le parcours jusqu'à la fin du test.
    actionTimeout: 4000,
    navigationTimeout: 10_000,
  },
  webServer: {
    command: "./.fixture.test -test.run TestE2EFixtureServer -test.timeout 0",
    env: { OPENCLOUD_E2E_ADDR: address },
    url: `http://${address}/`,
    reuseExistingServer: false,
    timeout: 60_000,
    // Les journaux du serveur noieraient le rapport ; en cas d'échec de démarrage, le délai le dit.
    // E2E_SERVER_LOGS=1 les laisse passer : sans elles, un serveur mort ne dit pas pourquoi.
    stdout: process.env.E2E_SERVER_LOGS === "1" ? "pipe" : "ignore",
    stderr: process.env.E2E_SERVER_LOGS === "1" ? "pipe" : "ignore",
  },
});
