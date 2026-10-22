import { expect, test } from "vitest";

import { auditPath } from "../api/audit";
import type { Translate } from "../i18n/context";
import { actorLabel, detailEntries, objectLabel, verbOf } from "./audit";

// Un faux catalogue : la clé et ses arguments, pour lire ce qui est demandé.
const t: Translate = (key, ...args) => [key, ...args].join(":");

test("the verb of an action is what follows the object kind", () => {
  expect(verbOf("probe.pause")).toBe("pause");
  expect(verbOf("api_token.create")).toBe("create");
  expect(verbOf("heartbeat.regenerate_token")).toBe("regenerate_token");
});

test("an actor reads as its kind, then its name when it has one", () => {
  expect(actorLabel(t, { actor_kind: "operator", actor_name: "lucas", details: {} })).toBe("audit.actor_operator · lucas");
  expect(actorLabel(t, { actor_kind: "rule", actor_name: "", details: {} })).toBe("audit.actor_rule");
});

test("an API token actor adds for whom mcp-maison says it acts", () => {
  expect(actorLabel(t, { actor_kind: "api", actor_name: "tok1", details: { on_behalf_of: "alice" } })).toBe("audit.actor_api · tok1 · audit.on_behalf_of:alice");
  expect(actorLabel(t, { actor_kind: "api", actor_name: "tok1", details: { on_behalf_of: "" } })).toBe("audit.actor_api · tok1");
  expect(actorLabel(t, { actor_kind: "mcp", actor_name: "tok0", details: {} })).toBe("audit.actor_mcp · tok0");
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
