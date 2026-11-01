import { appendFileSync, mkdirSync, readFileSync } from "node:fs";

// Les adresses du routeur, lues dans App.tsx : le registre que le parcours
// doit avoir toutes visitées. Une route par ligne ; une ouvrante (sans `/>`)
// donne son préfixe à ses filles jusqu'à `</Route>`.
const appPath = new URL("../../src/App.tsx", import.meta.url);

// Les routes qui ne sont qu'une redirection ou un outil de développement : le
// parcours les suit (leurs adresses sont testées par les tests de lib), elles
// ne sont pas un écran.
const notScreens = /Navigate|Redirect|VisualSystem|path="\*"|path="[^"]*\/\*"/;

export function declaredRoutes(): string[] {
  const routes: string[] = [];
  const prefixes: string[] = [];
  for (const raw of readFileSync(appPath, "utf8").split("\n")) {
    const line = raw.trim();
    if (line === "</Route>") {
      prefixes.pop();
      continue;
    }
    if (!line.startsWith("<Route")) {
      continue;
    }
    const path = /path="([^"]*)"/.exec(line)?.[1];
    const full = path === undefined ? (prefixes.at(-1) ?? "") : [prefixes.at(-1), path].filter(Boolean).join("/");
    const opens = !line.endsWith("/>");
    if (opens) {
      prefixes.push(full);
    }
    if (!notScreens.test(line) && line.includes("element=")) {
      routes.push(`/${full}`.replace(/\/$/, "") || "/");
    }
  }
  return routes;
}

export function matchesRoute(pattern: string, path: string): boolean {
  const wanted = pattern.split("/").filter(Boolean);
  const actual = path.split("/").filter(Boolean);
  return wanted.length === actual.length && wanted.every((segment, index) => segment.startsWith(":") || segment === actual[index]);
}

// Les identifiants du serveur (hexadécimaux, svc_…, prj_…, UUID, numériques)
// se ramènent à « :id » : une page de chaque forme suffit à la visite.
export function shapeOf(path: string): string {
  return path
    .split("/")
    .map((segment) => (/^(?:[0-9a-f]{8,}|[a-z]{2,4}_[0-9a-f]{8,}|[0-9a-f-]{36}|\d+)$/.test(segment) ? ":id" : segment))
    .join("/");
}

// Les adresses vues par le navigateur, gardées dans un fichier : Playwright
// relance son processus après un test en échec, la mémoire ne survit pas.
const visitsFile = new URL("../.results/visits.txt", import.meta.url);

export function recordVisit(path: string): void {
  mkdirSync(new URL(".", visitsFile), { recursive: true });
  appendFileSync(visitsFile, `${path}\n`);
}

export function recordedVisits(): string[] {
  try {
    return readFileSync(visitsFile, "utf8").split("\n").filter(Boolean);
  } catch {
    return [];
  }
}

// Les formes d'écran déjà parcourues, gardées de la même façon : un écran
// trouvé par les liens de plusieurs écrans n'est parcouru qu'une fois.
const exercisedFile = new URL("../.results/exercised.txt", import.meta.url);

// Vrai la première fois qu'une forme est réclamée.
export function claimShape(shape: string): boolean {
  let claimed: string[] = [];
  try {
    claimed = readFileSync(exercisedFile, "utf8").split("\n");
  } catch {
    mkdirSync(new URL(".", exercisedFile), { recursive: true });
  }
  if (claimed.includes(shape)) {
    return false;
  }
  appendFileSync(exercisedFile, `${shape}\n`);
  return true;
}
