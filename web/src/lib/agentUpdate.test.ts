import { describe, expect, it, test } from "vitest";

import type { ActionRun } from "../api/actions";
import { agentBehindServer, agentUpdateState } from "./agentUpdate";

function run(overrides: Partial<ActionRun>): ActionRun {
  return {
    id: "run1",
    kind: "agent.update",
    params: { version: "v0.0.2" },
    machine_id: "m1",
    machine_name: "vps",
    agent_id: "a1",
    service_id: null,
    state: "accepted",
    reason: "",
    seq: 1,
    requested_by: "operator:lucas",
    requested_at: "2026-10-29T10:00:00Z",
    not_after: "2026-10-29T10:02:00Z",
    finished_at: null,
    output: [],
    ...overrides,
  };
}

describe("agentUpdateState", () => {
  it("says nothing without any update order", () => {
    expect(agentUpdateState([])).toBeNull();
    expect(agentUpdateState([run({ kind: "agent.ping", state: "succeeded" })])).toBeNull();
  });

  it("waits for the confirmation while the order is in flight", () => {
    for (const state of ["queued", "sent", "accepted"] as const) {
      expect(agentUpdateState([run({ state })])).toEqual({ kind: "pending" });
    }
  });

  it("confirms a succeeded update or rollback", () => {
    expect(agentUpdateState([run({ state: "succeeded" })])).toEqual({ kind: "confirmed" });
    expect(agentUpdateState([run({ kind: "agent.rollback", params: {}, state: "succeeded" })])).toEqual({ kind: "confirmed" });
  });

  it("reads the automatic return with its cause and the refused version", () => {
    expect(agentUpdateState([run({ state: "failed", reason: "rolled_back_timeout" })])).toEqual({ kind: "rolled_back", target: "v0.0.2", cause: "timeout" });
    expect(agentUpdateState([run({ state: "failed", reason: "rolled_back_crash" })])).toEqual({ kind: "rolled_back", target: "v0.0.2", cause: "crash" });
  });

  it("flags a failed recovery loudly", () => {
    expect(agentUpdateState([run({ state: "failed", reason: "rollback_failed" })])).toEqual({ kind: "rollback_failed" });
  });

  it("ignores a refusal that changed nothing, and only reads the latest order", () => {
    expect(agentUpdateState([run({ state: "refused", reason: "guard_missing" })])).toBeNull();
    const runs = [run({ id: "new", state: "succeeded" }), run({ id: "old", state: "failed", reason: "rolled_back_crash" })];
    expect(agentUpdateState(runs)).toEqual({ kind: "confirmed" });
  });
});

describe("agentBehindServer", () => {
  test("une version plus ancienne est en retard, sur chaque composante", () => {
    expect(agentBehindServer("v0.2.0", "v0.4.1")).toBe(true);
    expect(agentBehindServer("v0.4.0", "v0.4.1")).toBe(true);
    expect(agentBehindServer("v0.9.9", "v1.0.0")).toBe(true);
  });

  test("la même version, ou une plus récente, n'est pas en retard", () => {
    expect(agentBehindServer("v0.4.1", "v0.4.1")).toBe(false);
    expect(agentBehindServer("v0.5.0", "v0.4.1")).toBe(false);
    expect(agentBehindServer("v1.0.0", "v0.9.9")).toBe(false);
  });

  test("la comparaison est numérique, pas alphabétique", () => {
    expect(agentBehindServer("v0.9.0", "v0.10.0")).toBe(true);
    expect(agentBehindServer("v0.10.0", "v0.9.0")).toBe(false);
  });

  test("une version qui n'est pas vX.Y.Z ne se compare pas", () => {
    expect(agentBehindServer("dev", "v0.4.1")).toBe(false);
    expect(agentBehindServer("v0.2.0", "")).toBe(false);
    expect(agentBehindServer("v0.2.0-rc1", "v0.4.1")).toBe(false);
  });
});
