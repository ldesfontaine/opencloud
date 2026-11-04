import { expect, test } from "vitest";

import { ApiError } from "../api/client";
import type { AgentAnchors } from "../api/machines";
import {
  anchorFieldsOf,
  anchorKinds,
  anchorKindsFor,
  anchorLabelKey,
  anchorOfReason,
  anchorsOf,
  areAnchorsModified,
  blockingAnchors,
  commandOfRefusal,
  splitAnchorValues,
  withKeyringRecipient,
} from "./anchors";

// La réponse figée de la fiche d'une machine (internal/server/testdata) : le
// registre du serveur y est écrit, type par type.
const goldenFile = import.meta.glob<string>("../../../internal/server/testdata/machine.golden.json", { eager: true, import: "default", query: "?raw" });

test("les types d'ancre du front sont ceux du registre du serveur, dans son ordre", () => {
  const golden = JSON.parse(Object.values(goldenFile)[0] ?? "{}") as { agent: { anchors: AgentAnchors } };
  expect(golden.agent.anchors.kinds.map((kind) => ({ name: kind.name, right: kind.right, max: kind.max }))).toEqual(anchorKinds.map((kind) => ({ ...kind })));
});

test("les clés du catalogue d'un type suivent son nom", () => {
  expect(anchorLabelKey("restic.target-cert")).toBe("anchor.label_restic_target_cert");
});

test("le formulaire ne demande que les ancres des droits cochés", () => {
  expect(anchorKindsFor(["probe.lan"]).map((kind) => kind.name)).toEqual([]);
  expect(anchorKindsFor(["backup.produce"]).map((kind) => kind.name)).toEqual(["backup.recipient", "backup.guest"]);
  expect(anchorKindsFor(["restic.produce"]).map((kind) => kind.name)).toEqual(["restic.target-cert"]);
});

test("les valeurs d'un champ se séparent par virgules, espaces ou lignes", () => {
  expect(splitAnchorValues(" 101, 102\n103  ,, ")).toEqual(["101", "102", "103"]);
  expect(splitAnchorValues("")).toEqual([]);
});

test("la requête ne porte que les ancres des droits cochés, non vides", () => {
  const fields = { "backup.recipient": "age1abc", "backup.guest": "101, 102", "restic.target-cert": "ab" };
  expect(anchorsOf(fields, ["backup.produce"])).toEqual({ "backup.recipient": ["age1abc"], "backup.guest": ["101", "102"] });
  expect(anchorsOf({ "backup.guest": "  " }, ["backup.produce"])).toEqual({});
  expect(anchorsOf(fields, [])).toEqual({});
});

test("cocher la production propose la clé du trousseau sans effacer la saisie", () => {
  expect(withKeyringRecipient({}, "age1key")).toEqual({ "backup.recipient": "age1key" });
  expect(withKeyringRecipient({ "backup.recipient": "age1mine" }, "age1key")).toEqual({ "backup.recipient": "age1mine" });
  expect(withKeyringRecipient({}, undefined)).toEqual({});
});

const reported: AgentAnchors = {
  reported: true,
  kinds: [
    { name: "backup.recipient", right: "backup.produce", max: 2, state: "set", values: ["age1key"] },
    { name: "backup.guest", right: "backup.produce", max: 32, state: "none", values: [] },
    { name: "restic.target-cert", right: "restic.produce", max: 4, state: "unreadable", values: [] },
  ],
};

test("un ré-enrôlement redonne les ancres posées, pas les illisibles", () => {
  expect(anchorFieldsOf(reported)).toEqual({ "backup.recipient": "age1key" });
  expect(anchorFieldsOf(undefined)).toEqual({});
  expect(areAnchorsModified({ "backup.recipient": "age1key" }, anchorFieldsOf(reported))).toBe(false);
  expect(areAnchorsModified({ "backup.recipient": "age1key", "backup.guest": "101" }, anchorFieldsOf(reported))).toBe(true);
});

test("deux états distincts bloquent un ordre : aucune posée, illisibles", () => {
  expect(blockingAnchors(reported, ["backup.produce", "restic.produce"]).map((kind) => `${kind.name}:${kind.state}`)).toEqual(["backup.guest:none", "restic.target-cert:unreadable"]);
  // Sans le droit coché, l'ancre ne bloque rien.
  expect(blockingAnchors(reported, ["observe"])).toEqual([]);
  // Un agent d'avant les ancres ne les applique pas : rien ne bloque.
  expect(blockingAnchors({ reported: false, kinds: reported.kinds }, ["backup.produce"])).toEqual([]);
});

test("une raison d'ordre refusé par une ancre nomme son type", () => {
  expect(anchorOfReason("anchor_outside:backup.guest")).toEqual({ why: "outside", name: "backup.guest" });
  expect(anchorOfReason("anchor_missing:restic.target-cert")).toEqual({ why: "missing", name: "restic.target-cert" });
  expect(anchorOfReason("right_missing:backup.produce")).toBeNull();
  expect(anchorOfReason("anchor_missing")).toBeNull();
});

test("la commande d'un refus vient du corps de la réponse", () => {
  expect(commandOfRefusal(new ApiError(409, "anchor.backup_guest_missing", { error: "anchor.backup_guest_missing", command: "/x agent trust add backup.guest 101" }))).toBe("/x agent trust add backup.guest 101");
  expect(commandOfRefusal(new ApiError(409, "action.right_missing"))).toBeNull();
  expect(commandOfRefusal(new ApiError(409, "x", { command: "" }))).toBeNull();
  expect(commandOfRefusal("anchor.backup_guest_missing")).toBeNull();
  expect(commandOfRefusal(null)).toBeNull();
});
