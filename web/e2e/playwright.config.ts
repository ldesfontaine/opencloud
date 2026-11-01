import { defineConfig } from "@playwright/test";

// Le serveur de données fictives est le test Go TestE2EFixtureServer, compilé
// par `make e2e` en .fixture.test : le vrai serveur, sa vraie base, l'horloge
// figée, rien de réel.
const address = "127.0.0.1:5199";

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
    stdout: "ignore",
    stderr: "ignore",
  },
});
