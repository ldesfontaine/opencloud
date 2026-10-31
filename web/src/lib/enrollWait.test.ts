import { expect, test } from "vitest";

import type { Agent, Machine } from "../api/machines";
import { enrollProgress, type EnrollTarget } from "./enrollWait";

function agent(id: string, revokedAt: string | null = null): Agent {
  return { id, revoked_at: revokedAt } as Agent;
}

function machine(id: string, name: string, agentOf: Agent | null): Machine {
  return { id, name, agent: agentOf } as Machine;
}

const expiresAt = Date.parse("2026-10-31T12:00:00Z");
const before = expiresAt - 60_000;
const after = expiresAt + 1_000;

const added: EnrollTarget = { kind: "new", name: "vps-1", knownIDs: ["m1"] };
const reenrolled: EnrollTarget = { kind: "reenroll", machineID: "m1", previousAgentID: "a1" };

test("machine neuve : attend tant qu'aucune machine de ce nom n'a d'agent actif", () => {
  expect(enrollProgress(added, [], expiresAt, before)).toEqual({ state: "waiting" });
  expect(enrollProgress(added, [machine("m2", "vps-1", null)], expiresAt, before)).toEqual({ state: "waiting" });
  expect(enrollProgress(added, [machine("m2", "vps-1", agent("a2", "2026-10-31T10:00:00Z"))], expiresAt, before)).toEqual({ state: "waiting" });
});

test("machine neuve : connectée dès que la machine de ce nom a un agent actif", () => {
  const arrived = machine("m2", "vps-1", agent("a2"));
  expect(enrollProgress(added, [machine("m1", "autre", agent("a1")), arrived], expiresAt, before)).toEqual({ state: "connected", machine: arrived });
});

test("machine neuve : une machine déjà connue sous ce nom n'est pas l'arrivée", () => {
  expect(enrollProgress(added, [machine("m1", "vps-1", agent("a1"))], expiresAt, before)).toEqual({ state: "waiting" });
});

test("ré-enrôlement : attend l'ancien agent, connecté dès qu'un autre est actif", () => {
  expect(enrollProgress(reenrolled, [machine("m1", "vps-1", agent("a1"))], expiresAt, before)).toEqual({ state: "waiting" });
  const replaced = machine("m1", "vps-1", agent("a2"));
  expect(enrollProgress(reenrolled, [replaced], expiresAt, before)).toEqual({ state: "connected", machine: replaced });
});

test("ré-enrôlement d'une machine sans agent actif : le premier agent actif est l'arrivée", () => {
  const revoked: EnrollTarget = { kind: "reenroll", machineID: "m1", previousAgentID: "a1" };
  expect(enrollProgress(revoked, [machine("m1", "vps-1", agent("a1", "2026-10-31T10:00:00Z"))], expiresAt, before)).toEqual({ state: "waiting" });
  const never: EnrollTarget = { kind: "reenroll", machineID: "m1", previousAgentID: null };
  const first = machine("m1", "vps-1", agent("a9"));
  expect(enrollProgress(never, [first], expiresAt, before)).toEqual({ state: "connected", machine: first });
});

test("jeton expiré : l'attente le dit, sauf si l'agent est arrivé à temps", () => {
  expect(enrollProgress(added, [], expiresAt, after)).toEqual({ state: "expired" });
  const arrived = machine("m2", "vps-1", agent("a2"));
  expect(enrollProgress(added, [arrived], expiresAt, after)).toEqual({ state: "connected", machine: arrived });
});
