import { expect, test } from "vitest";

import type { GapsResponse, UndeclaredGroup } from "../api/types";
import { adoptIntoPath, adoptPath, adoptionTarget, containerCount, gapsByService, gapsPath, gapTone, ignoreBody, ignoredCount, shownUndeclared, unignorePath } from "./gaps";

const group = (overrides: Partial<UndeclaredGroup>): UndeclaredGroup => ({
  machine_id: "m1",
  machine_name: "alpha",
  key_kind: "compose_project",
  key_value: "blog",
  ignored: false,
  service_id: "",
  containers: [{ id: "c1", name: "blog-web-1", state: "running", image: "nginx:1", compose_service: "web" }],
  ...overrides,
});

test("gapTone teinte chaque état de l'écart", () => {
  expect(gapTone("conform")).toBe("ok");
  expect(gapTone("absent")).toBe("danger");
  expect(gapTone("diverged")).toBe("warn");
  expect(gapTone("unverifiable")).toBe("warn");
  expect(gapTone("unobserved")).toBe("neutral");
});

test("gapsByService indexe par service et supporte l'absence de réponse", () => {
  const gaps: GapsResponse = { services: [{ service_id: "svc_1", state: "conform", weak: false, units: [] }], undeclared: [] };
  expect(gapsByService(gaps).get("svc_1")?.state).toBe("conform");
  expect(gapsByService(gaps).get("svc_2")).toBeUndefined();
  expect(gapsByService(null).size).toBe(0);
});

test("shownUndeclared cache les ignorés tant qu'on ne les demande pas", () => {
  const groups = [group({}), group({ key_value: "old", ignored: true })];
  expect(shownUndeclared(groups, false)).toHaveLength(1);
  expect(shownUndeclared(groups, true)).toHaveLength(2);
  expect(ignoredCount(groups)).toBe(1);
  expect(containerCount(groups)).toBe(2);
});

test("adopter passe l'identifiant du conteneur, encodé", () => {
  expect(adoptPath("abc")).toBe("/services/annuaire/nouveau?container=abc");
  expect(adoptPath("a b&c")).toBe("/services/annuaire/nouveau?container=a%20b%26c");
  expect(adoptIntoPath("svc_1", "abc")).toBe("/services/annuaire/svc_1/modifier?container=abc");
  expect(adoptionTarget(group({}))).toBe("c1");
  expect(adoptionTarget(group({ containers: [] }))).toBe("");
});

test("ignorer envoie la clé d'observation, et ne plus ignorer la met en requête", () => {
  expect(ignoreBody(group({}))).toEqual({ machine_id: "m1", kind: "compose_project", value: "blog" });
  expect(unignorePath(group({ key_kind: "container_name", key_value: "a b" }))).toBe("/api/directory/ignored?machine=m1&kind=container_name&value=a+b");
});

test("gapsPath ne porte que les filtres choisis", () => {
  expect(gapsPath("", "")).toBe("/api/directory/gaps");
  expect(gapsPath("m1", "")).toBe("/api/directory/gaps?machine=m1");
  expect(gapsPath("m1", "prj_1")).toBe("/api/directory/gaps?machine=m1&project=prj_1");
});
