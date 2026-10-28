import { expect, test } from "vitest";

import { auditPath } from "../api/audit";
import type { Translate } from "../i18n/context";
import { actorLabel, actorParts, actorTone, detailEntries, objectLabel, verbOf } from "./audit";

// Un faux catalogue : la clé et ses arguments, pour lire ce qui est demandé.
const t: Translate = (key, ...args) => [key, ...args].join(":");

test("the verb of an action is what follows the object kind", () => {
  expect(verbOf("probe.pause")).toBe("pause");
  expect(verbOf("api_token.create")).toBe("create");
  expect(verbOf("heartbeat.regenerate_token")).toBe("regenerate_token");
});

const noLabel = { actor_label: null };

test("an actor reads as its kind, then its name when it has one", () => {
  expect(actorLabel(t, { actor_kind: "operator", actor_name: "lucas", ...noLabel, details: {} })).toBe("audit.actor_operator · lucas");
  expect(actorLabel(t, { actor_kind: "rule", actor_name: "", ...noLabel, details: {} })).toBe("audit.actor_rule");
});

test("an API token reads by its name at the time, then for whom it acts", () => {
  expect(actorLabel(t, { actor_kind: "api", actor_name: "tok1", actor_label: "agent-veille", details: { on_behalf_of: "alice" } })).toBe(
    "audit.actor_api · agent-veille · audit.on_behalf_of:alice",
  );
  // Un jeton qui ne dit pas pour qui se voit : « inconnu », jamais rien.
  expect(actorLabel(t, { actor_kind: "api", actor_name: "tok1", actor_label: "agent-veille", details: { on_behalf_of: "" } })).toBe(
    "audit.actor_api · agent-veille · audit.on_behalf_of_unknown",
  );
  expect(actorLabel(t, { actor_kind: "api", actor_name: "tok1", actor_label: "agent-veille", details: {} })).toBe("audit.actor_api · agent-veille · audit.on_behalf_of_unknown");
});

test("a line from before the label falls back on the token id", () => {
  expect(actorParts({ actor_kind: "api", actor_name: "tok1", actor_label: null, details: { on_behalf_of: "alice" } })).toEqual({ kind: "api", name: "tok1", forWhom: "alice" });
  expect(actorParts({ actor_kind: "api", actor_name: "tok1", actor_label: "", details: {} }).name).toBe("tok1");
});

test("only a token has a for-whom; a person's line cannot pass for one", () => {
  expect(actorParts({ actor_kind: "operator", actor_name: "lucas", ...noLabel, details: { on_behalf_of: "alice" } }).forWhom).toBeNull();
  expect(actorLabel(t, { actor_kind: "mcp", actor_name: "tok0", ...noLabel, details: {} })).toBe("audit.actor_mcp · tok0");
});

test("each kind has its own tone", () => {
  expect(actorTone("api")).toBe("accent");
  expect(actorTone("operator")).toBe("ok");
  expect(actorTone("unknown")).toBe("danger");
  expect(actorTone("agent")).toBe("neutral");
});

test("an object reads as its copied name, else its id", () => {
  expect(objectLabel({ object_name: "api", object_id: "p1" })).toBe("api");
  expect(objectLabel({ object_name: "", object_id: "p1" })).toBe("p1");
});

test("details come sorted by name", () => {
  expect(detailEntries({ scope: "read", expires_at: "never" })).toEqual([
    ["expires_at", "never"],
    ["scope", "read"],
  ]);
});

test("the path carries the cursor and the followed object", () => {
  expect(auditPath(null, null)).toBe("/api/audit");
  expect(auditPath(42, null)).toBe("/api/audit?before=42");
  expect(auditPath(null, { kind: "probe", id: "p1", name: "api" })).toBe("/api/audit?object_kind=probe&object_id=p1");
});

test("the path carries the actor filters", () => {
  expect(auditPath(null, null, { kind: "api", search: "" })).toBe("/api/audit?actor_kind=api");
  expect(auditPath(7, null, { kind: "", search: "agent veille" })).toBe("/api/audit?actor=agent+veille&before=7");
});
