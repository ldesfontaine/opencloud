import { expect, test } from "vitest";

import type { Machine, RightState } from "../api/machines";
import { fitOf } from "./rightFit";

const tables = import.meta.glob<FitCase[]>("../../../internal/machine/testdata/right_fit_cases.json", { eager: true, import: "default" });
const cases = Object.values(tables).flat();

// Les cas de la table que le serveur juge aussi (machine/fit_test.go).
interface FitCase {
  name: string;
  agent: { revoked: boolean; reported: boolean; capabilities: { name: string; paths: string[]; unavailable: string }[] } | null;
  right: string;
  expect: { state: RightState; reason: string; path: string };
}

function agentOf(described: FitCase["agent"]): Machine["agent"] {
  if (described === null) {
    return null;
  }
  return {
    id: "a1",
    version: "v0.0.1",
    enrolled_at: "2026-11-01T10:00:00Z",
    revoked_at: described.revoked ? "2026-11-02T10:00:00Z" : null,
    capabilities: described.capabilities,
    reported_at: described.reported ? "2026-11-01T10:00:00Z" : null,
    requested: [],
    capabilities_differ: false,
    server_key_fingerprint: "",
    run_as: null,
    rollback_capable: true,
    anchors: { reported: false, kinds: [] },
  };
}

test("la table commune a des cas", () => {
  expect(cases.length).toBeGreaterThan(0);
});

test.each(cases as FitCase[])("fitOf : $name", (fitCase) => {
  const fit = fitOf(agentOf(fitCase.agent), fitCase.right);
  expect({ state: fit.state, reason: fit.reason, path: fit.path }).toEqual(fitCase.expect);
});
