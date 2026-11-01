import { expect, test } from "vitest";

import type { ContainerDirectory, GapsResponse, UndeclaredContainer, UndeclaredGroup } from "../api/directory";
import { acceptanceTarget, containerCount, editBeforeAcceptPath, gapsByService, gapTone, hasProposals, ignoredOf, ignoreKeyOf, isOrphan, orphanKey, orphanText, pickableContainers, proposalsOf } from "./gaps";

const undeclaredContainer = (overrides: Partial<UndeclaredContainer>): UndeclaredContainer => ({
  id: "c1",
  name: "blog-web-1",
  state: "running",
  image: "nginx:1",
  compose_service: "web",
  label_service: "",
  label_unit: "",
  label_problem: "",
  ...overrides,
});

const group = (overrides: Partial<UndeclaredGroup>): UndeclaredGroup => ({
  machine_id: "m1",
  machine_name: "alpha",
  key_kind: "compose_service",
  key_value: "blog/web",
  project: "blog",
  compose_service: "web",
  ignored: false,
  adoptable: true,
  containers: [undeclaredContainer({})],
  ...overrides,
});

const directoryOf = (overrides: Partial<ContainerDirectory>): ContainerDirectory => ({
  declared: false,
  service_id: "",
  service_name: "",
  unit_id: "",
  unit_name: "",
  match_level: "",
  weak: false,
  key_kind: "container_name",
  key_value: "web",
  ignored: false,
  label_service: "",
  label_unit: "",
  label_problem: "",
  label_machine_name: "",
  ...overrides,
});

// Un traducteur de pacotille : la clé, puis ses arguments.
const translate = (key: string, ...args: (string | number)[]) => [key, ...args].join("|");

test("gapTone teinte chaque état de l'écart", () => {
  expect(gapTone("conform")).toBe("ok");
  expect(gapTone("absent")).toBe("danger");
  expect(gapTone("diverged")).toBe("warn");
  expect(gapTone("unverifiable")).toBe("warn");
  expect(gapTone("unobserved")).toBe("neutral");
});

test("gapsByService indexe par service et supporte l'absence de réponse", () => {
  const gaps: GapsResponse = { services: [{ service_id: "svc_1", state: "conform", observed: true, weak: false, units: [] }], undeclared: [] };
  expect(gapsByService(gaps).get("svc_1")?.state).toBe("conform");
  expect(gapsByService(gaps).get("svc_2")).toBeUndefined();
  expect(gapsByService(null).size).toBe(0);
});

test("une proposition n'est pas un ignoré : deux listes, jamais l'une dans l'autre", () => {
  const groups = [group({}), group({ key_value: "old", ignored: true })];
  expect(proposalsOf(groups).map((item) => item.key_value)).toEqual(["blog/web"]);
  expect(ignoredOf(groups).map((item) => item.key_value)).toEqual(["old"]);
  expect(containerCount(groups)).toBe(2);
});

test("modifier avant d'accepter passe l'identifiant du conteneur, encodé", () => {
  expect(editBeforeAcceptPath("abc")).toBe("/services/annuaire/nouveau?container=abc");
  expect(editBeforeAcceptPath("a b&c")).toBe("/services/annuaire/nouveau?container=a%20b%26c");
  expect(acceptanceTarget(group({}))).toBe("c1");
  expect(acceptanceTarget(group({ containers: [] }))).toBe("");
});

test("ignorer envoie la clé d'observation du groupe", () => {
  expect(ignoreKeyOf(group({}))).toEqual({ machine_id: "m1", kind: "compose_service", value: "blog/web" });
});

test("un conteneur étiqueté ne s'accepte pas : le groupe passe par un voisin sans label", () => {
  const orphan = undeclaredContainer({ id: "c1", label_service: "svc_x", label_unit: "web", label_problem: "service_unknown" });
  const plain = undeclaredContainer({ id: "c2", name: "blog-db-1" });
  expect(acceptanceTarget(group({ containers: [orphan, plain] }))).toBe("c2");
  expect(acceptanceTarget(group({ containers: [orphan], adoptable: false }))).toBe("");
});

test("l'export avec propositions n'est offert que s'il y a un groupe acceptable non ignoré", () => {
  expect(hasProposals([group({})])).toBe(true);
  expect(hasProposals([group({ adoptable: false }), group({ ignored: true })])).toBe(false);
  expect(hasProposals([])).toBe(false);
});

test("orphanKey compose la clé du motif, orphanText nomme la machine d'un service ailleurs", () => {
  expect(orphanKey("incomplete")).toBe("gap.orphan_incomplete");
  expect(orphanKey("unit_binary")).toBe("gap.orphan_unit_binary");
  expect(orphanKey("invalid")).toBe("gap.orphan_invalid");
  expect(orphanText(translate, directoryOf({}))).toBe("");
  expect(orphanText(translate, directoryOf({ label_problem: "unit_unknown" }))).toBe("gap.orphan_unit_unknown");
  expect(orphanText(translate, directoryOf({ label_problem: "service_unknown" }))).toBe("gap.orphan_service_unknown");
  expect(orphanText(translate, directoryOf({ label_problem: "service_unknown", label_machine_name: "vps-2" }))).toBe("gap.orphan_elsewhere|vps-2");
  // Le nom d'une autre machine ne se dit que pour un service inconnu de la machine du conteneur.
  expect(orphanText(translate, directoryOf({ label_problem: "unit_unknown", label_machine_name: "vps-2" }))).toBe("gap.orphan_unit_unknown");
});

test("isOrphan suit le motif, même quand aucun label n'est lisible", () => {
  expect(isOrphan(null)).toBe(false);
  expect(isOrphan(directoryOf({}))).toBe(false);
  expect(isOrphan(directoryOf({ label_service: "svc_x", label_problem: "incomplete" }))).toBe(true);
  expect(isOrphan(directoryOf({ label_problem: "invalid" }))).toBe(true);
  // Rattaché par son label : porte des labels, n'est pas orphelin.
  expect(isOrphan(directoryOf({ label_service: "svc_x", label_unit: "web" }))).toBe(false);
});

test("l'assistant ne propose que ce qui s'accepte : un conteneur par service Compose, ni ignoré ni orphelin", () => {
  const groups = [
    group({}),
    group({ key_value: "blog/db", compose_service: "db", containers: [undeclaredContainer({ id: "c2", name: "blog-db-1" })] }),
    group({ key_value: "blog/old", compose_service: "old", ignored: true, containers: [undeclaredContainer({ id: "c3" })] }),
    group({ key_value: "blog/lost", compose_service: "lost", adoptable: false, containers: [undeclaredContainer({ id: "c4", label_service: "svc_x", label_unit: "lost" })] }),
    group({ key_kind: "container_name", key_value: "whoami", project: "", compose_service: "", machine_name: "beta", containers: [undeclaredContainer({ id: "c5", name: "whoami" })] }),
  ];
  expect(pickableContainers(groups)).toEqual([
    { value: "c2", label: "db (blog)", hint: "alpha" },
    { value: "c1", label: "web (blog)", hint: "alpha" },
    { value: "c5", label: "whoami", hint: "beta" },
  ]);
});
