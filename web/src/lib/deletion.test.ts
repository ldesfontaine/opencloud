import { expect, test } from "vitest";

import { ApiError } from "../api/client";
import { planOfRefusal, type DeletionPlan } from "../api/deletion";
import type { Translate } from "../i18n/context";
import { blockerText, countsText, isBarePlan } from "./deletion";

// Un faux catalogue : la clé et ses arguments, pour lire ce qui est demandé.
const t: Translate = (key, ...args) => [key, ...args].join(":");

const probes = { kind: "probes", objects: [{ id: "p1", name: "site" }, { id: "p2", name: "api" }] };

const blocked: DeletionPlan = {
  blocked: true,
  blockers: [probes],
  removes: [{ kind: "probe_results", count: 1204 }],
  detaches: [{ kind: "alerts", count: 1 }],
};

test("a blocker names each object that holds the deletion back", () => {
  expect(blockerText(t, "en", probes)).toBe("deletion.kind_probes:2 : site, api");
  expect(blockerText(t, "en", { kind: "components", objects: [{ id: "c1", name: "Nuage" }] })).toBe("deletion.kind_components_one:1 : Nuage");
});

test("what goes or stays is counted, in the order of the plan", () => {
  expect(countsText(t, "en", blocked.removes)).toBe("deletion.kind_probe_results:1,204");
  expect(countsText(t, "en", [...blocked.removes, ...blocked.detaches])).toBe("deletion.kind_probe_results:1,204, deletion.kind_alerts_one:1");
  expect(countsText(t, "en", [])).toBe("");
});

test("a plan with nothing to hold, take or detach is bare", () => {
  expect(isBarePlan(blocked)).toBe(false);
  expect(isBarePlan({ blocked: false, blockers: [], removes: [], detaches: [] })).toBe(true);
});

test("a refused deletion carries its plan in the 409 body, nothing else does", () => {
  expect(planOfRefusal(new ApiError(409, "machine.has_probes", { error: "machine.has_probes", plan: blocked }))).toEqual(blocked);
  expect(planOfRefusal(new ApiError(409, "directory.name_taken", { error: "directory.name_taken" }))).toBeNull();
  expect(planOfRefusal(new ApiError(422, "probe.name_invalid", { error: "probe.name_invalid", plan: blocked }))).toBeNull();
  expect(planOfRefusal(new Error("x"))).toBeNull();
});
