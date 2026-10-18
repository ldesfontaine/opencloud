import { expect, test } from "vitest";

import type { Apply } from "../api/types";
import type { Translate } from "../i18n/context";
import { actorText, carriesText, countedText, formatCount, changeLabel, failedObjectText, fieldText, shortHash, stateTone, summaryText, totalChanges, writtenText } from "./manifest";

// Un faux catalogue : la clé et ses arguments, pour lire ce qui est demandé.
const t: Translate = (key, ...args) => [key, ...args].join(":");

test("a pass reads as created, changed, deleted", () => {
  expect(summaryText({ create: 1, update: 2, delete: 0 })).toBe("+1 ~2 −0");
  expect(totalChanges({ create: 1, update: 2, delete: 3 })).toBe(6);
});

test("a state has the tone of its pill", () => {
  expect(stateTone("applied")).toBe("ok");
  expect(stateTone("failed")).toBe("danger");
  expect(stateTone("confirmed")).toBe("warn");
});

test("a change is named by its kind then its name", () => {
  expect(changeLabel(t, { kind: "probe", name: "wiki-http" })).toBe("manifest.kind_probe wiki-http");
  expect(failedObjectText(t, "service/app-1/wiki")).toBe("manifest.kind_service app-1/wiki");
  expect(failedObjectText(t, "")).toBe("");
});

test("a field that appears or disappears shows an empty set", () => {
  expect(fieldText({ field: "interval", before: "1m", after: "30s" })).toBe("interval : 1m → 30s");
  expect(fieldText({ field: "public_url", before: "", after: "https://a.fr" })).toBe("public_url : ∅ → https://a.fr");
  expect(fieldText({ field: "tls", before: "true", after: "" })).toBe("tls : true → ∅");
});

test("what a deletion takes along is listed with its counts", () => {
  expect(
    carriesText(t, "en", [
      { what: "probe_results", count: 1204 },
      { what: "probe_days", count: 80 },
    ]),
  ).toBe("manifest.carries_probe_results:1,204, manifest.carries_probe_days:80");
  expect(carriesText(t, "en", [])).toBe("");
});

test("a single item takes the singular text, and thousands are separated like the language", () => {
  expect(carriesText(t, "en", [{ what: "units", count: 1 }])).toBe("manifest.carries_units_one:1");
  expect(countedText(t, "fr", "manifest.carries_units", 0)).toBe("manifest.carries_units_one:0");
  expect(countedText(t, "en", "manifest.carries_units", 0)).toBe("manifest.carries_units:0");
  expect(formatCount("en", 1204)).toBe("1,204");
  expect(formatCount("fr", 1204).replace(/\s/g, " ")).toBe("1 204");
  expect(writtenText(t, "en", 2, 1204)).toBe("manifest.written:2:1,204");
  expect(writtenText(t, "en", 1, 3)).toBe("manifest.written_one:1:3");
});

test("a hash is cut to what recognises a file", () => {
  expect(shortHash("sha256:1a2b3c4d5e6f7a8b9c0d")).toBe("sha256:1a2b3c4d…");
  expect(shortHash("sha256:abc")).toBe("sha256:abc");
});

test("the actor is the kind, then the account when known", () => {
  const cli: Pick<Apply, "actor_kind" | "actor_name"> = { actor_kind: "cli", actor_name: "root" };
  expect(actorText(t, cli)).toBe("manifest.actor_cli · root");
  expect(actorText(t, { actor_kind: "operator", actor_name: "" })).toBe("manifest.actor_operator");
});
