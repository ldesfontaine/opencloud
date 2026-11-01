import { test as base, type Page } from "@playwright/test";

import { isKnown } from "./known";

// Une anomalie trouvée par le parcours : où, et ce qui s'est passé.
export interface Finding {
  where: string;
  what: string;
}

// Le registre des anomalies d'un test. Une anomalie connue (known.ts) est
// tolérée, l'inconnue fait échouer le test : le parcours reste vert tant que
// rien de nouveau ne casse, et la liste des connues est celle des correctifs
// à faire.
export class Findings {
  readonly all: Finding[] = [];
  where = "";

  add(what: string, where = this.where): void {
    const finding = { where, what: what.split("\n")[0]?.slice(0, 220) ?? what };
    if (!this.all.some((seen) => seen.where === finding.where && seen.what === finding.what)) {
      this.all.push(finding);
    }
  }

  unknown(): Finding[] {
    return this.all.filter((finding) => !isKnown(finding));
  }
}

function isAborted(error: string | undefined): boolean {
  return error === "net::ERR_ABORTED" || error === "net::ERR_NETWORK_CHANGED";
}

// Écoute la page : erreur de script, message d'erreur de la console, requête
// perdue, lecture refusée (GET en 4xx/5xx) ou écriture en 5xx. Une écriture
// refusée en 4xx est la validation du serveur : elle se lit dans le
// formulaire, pas ici.
function watch(page: Page, findings: Findings): void {
  page.on("pageerror", (error) => findings.add(`erreur de script : ${error.message}`));
  page.on("console", (message) => {
    if (message.type() !== "error") {
      return;
    }
    // « Failed to load resource » double la réponse HTTP, jugée plus bas.
    if (message.text().startsWith("Failed to load resource")) {
      return;
    }
    findings.add(`console.error : ${message.text()}`);
  });
  page.on("requestfailed", (request) => {
    if (!isAborted(request.failure()?.errorText)) {
      findings.add(`requête perdue : ${request.method()} ${new URL(request.url()).pathname} ${request.failure()?.errorText ?? ""}`);
    }
  });
  page.on("response", (response) => {
    const status = response.status();
    const method = response.request().method();
    const refused = status >= 500 || (method === "GET" && status >= 400);
    if (refused) {
      findings.add(`${method} ${new URL(response.url()).pathname} répond ${status}`);
    }
  });
}

export const test = base.extend<{ findings: Findings }>({
  findings: async ({ page }, use) => {
    const findings = new Findings();
    watch(page, findings);
    await use(findings);
    const unknown = findings.unknown();
    if (unknown.length > 0) {
      throw new Error(`anomalies inconnues :\n${unknown.map((finding) => `  [${finding.where}] ${finding.what}`).join("\n")}`);
    }
  },
});

export { expect } from "@playwright/test";
