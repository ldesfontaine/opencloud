import { expect, test } from "vitest";

import { writes } from "./routes";

// La parité UI / MCP (doc refonte-ux, §3.7) : toute écriture d'écran est une
// route de l'API, tout verbe du registre a son geste d'écran. Le versant Go
// (une route d'écriture sans appelant) est internal/server/parity_test.go.

const routesFile = import.meta.glob<string>("../../../internal/server/testdata/routes.txt", { eager: true, import: "default", query: "?raw" });
const verbsFile = import.meta.glob<string>("../../../internal/server/testdata/verbs.golden.json", { eager: true, import: "default", query: "?raw" });
const sources = import.meta.glob<string>(["./*.ts", "!./*.test.ts", "!./routes.ts"], { eager: true, import: "default", query: "?raw" });

function onlyContent(files: Record<string, string>): string {
  const contents = Object.values(files);
  expect(contents).toHaveLength(1);
  return contents[0] ?? "";
}

const serverRoutes = onlyContent(routesFile)
  .split("\n")
  .filter((line) => line !== "");

interface Verb {
  id: string;
  route: string;
  reverse: string;
}

const verbs = (JSON.parse(onlyContent(verbsFile)) as { verbs: Verb[] }).verbs;

test("chaque écriture déclarée par le front est une route du serveur", () => {
  const unknown = writes.filter((pattern) => !serverRoutes.includes(pattern));
  expect(unknown).toEqual([]);
});

test("chaque écriture déclarée a un appelant dans api/", () => {
  const code = Object.values(sources).join("\n");
  const unused = writes.filter((pattern) => !code.includes(`"${pattern}"`));
  expect(unused).toEqual([]);
});

test("chaque verbe du registre a son geste d'écran", () => {
  expect(verbs.length).toBeGreaterThan(0);
  const withoutGesture = verbs.filter((verb) => !(writes as readonly string[]).includes(verb.route)).map((verb) => verb.id);
  expect(withoutGesture).toEqual([]);
});
