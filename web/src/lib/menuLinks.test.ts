import { createRoutesFromChildren, matchRoutes } from "react-router";
import type { ReactElement } from "react";
import { expect, test } from "vitest";

import { App } from "../App";
import { settingsBlockAnchors, settingsSections } from "./settingsSections";

// Les renvois de menu des textes (internal/lang/links.go) sont figés dans un
// golden par le test Go. Ici : chaque adresse est une route de l'interface et
// chaque ancre un élément qui existe encore. Un menu renommé ou un bloc
// supprimé fait échouer ce test, pas l'opérateur.
const goldens = import.meta.glob<{ id: string; path: string; label: string }[]>("../../../internal/lang/testdata/links.golden.json", {
  eager: true,
  import: "default",
});
const links = Object.values(goldens)[0] ?? [];

const routes = createRoutesFromChildren((App() as ReactElement<{ children: React.ReactNode }>).props.children);
const anchors = new Set<string>([...settingsSections.map((section) => section.id), ...Object.values(settingsBlockAnchors)]);

test("le golden des renvois est lu", () => {
  expect(links.length).toBeGreaterThan(0);
});

test.each(links)("le renvoi $id mène à une route qui existe", ({ path }) => {
  const [pathname = ""] = path.split("#");
  expect(matchRoutes(routes, pathname)).not.toBeNull();
});

test.each(links.filter((link) => link.path.includes("#")))("l'ancre du renvoi $id existe dans Paramètres", ({ path }) => {
  const [pathname = "", anchor = ""] = path.split("#");
  expect(pathname).toBe("/parametres");
  expect(anchors.has(anchor)).toBe(true);
});
