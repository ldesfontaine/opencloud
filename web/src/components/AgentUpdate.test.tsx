import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router";
import { beforeEach, expect, test, vi } from "vitest";

import type { ActionRun, AgentMigration, AgentUpdateOffer } from "../api/actions";
import type { Agent } from "../api/machines";
import { AgentUpdate } from "./AgentUpdate";

// Sans serveur, la traduction rend la clé et ses arguments : le test lit ce que la fiche demande.
vi.mock("../i18n/context", () => ({
  useT: () => (key: string, ...args: (string | number)[]) => (args.length === 0 ? key : `${key}(${args.join("|")})`),
  useI18n: () => ({ language: "fr" }),
}));

vi.mock("../hooks/useAction", () => ({
  useAction: () => ({ run: () => {}, busy: false, error: null }),
}));

let offer: AgentUpdateOffer;
let runs: ActionRun[] = [];
const migration: AgentMigration = { version: "v0.0.3", commands: "sudo systemctl stop x\nsudo install guard" };

vi.mock("../api/actions", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/actions")>()),
  useAgentUpdateOffer: () => ({ data: offer, error: null, reload: () => {} }),
  useMachineActions: () => ({ data: { runs }, error: null, reload: () => {} }),
  useAgentMigration: () => ({ data: migration, error: null, reload: () => {} }),
}));

function agent(over: Partial<Agent> = {}): Agent {
  return {
    id: "agt",
    version: "v0.0.2",
    enrolled_at: "2026-10-01T00:00:00Z",
    revoked_at: null,
    capabilities: [],
    reported_at: "2026-10-01T00:00:00Z",
    requested: [],
    capabilities_differ: false,
    server_key_fingerprint: "",
    run_as: null,
    rollback_capable: true,
    ...over,
  };
}

function run(over: Partial<ActionRun>): ActionRun {
  return {
    id: "run1",
    kind: "agent.update",
    params: { version: "v0.0.3" },
    machine_id: "m1",
    machine_name: "vps",
    agent_id: "agt",
    service_id: null,
    state: "accepted",
    reason: "",
    seq: 1,
    requested_by: "operator:lucas",
    requested_at: "2026-10-29T10:00:00Z",
    not_after: "2026-10-29T10:02:00Z",
    finished_at: null,
    output: [],
    ...over,
  };
}

function render(over: { agent?: Agent; updateLast?: boolean; online?: boolean } = {}): string {
  return renderToStaticMarkup(
    <MemoryRouter>
      <AgentUpdate machineID="m1" agent={over.agent ?? agent()} updateLast={over.updateLast ?? false} online={over.online ?? true} />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  offer = { current: "v0.0.2", target: "v0.0.3", arch: "amd64", available: true, reason: "" };
  runs = [];
});

test("un agent migré a le bouton de mise à jour et celui du retour arrière", () => {
  const markup = render();
  expect(markup).toContain("machine.agent_update(v0.0.3)");
  expect(markup).toContain("machine.agent_rollback");
  expect(markup).toContain("machine.agent_rollback_capable_yes");
  expect(markup).not.toContain("machine.agent_migration_title");
});

test("un agent pas migré ne se met pas à jour : la fiche dit pourquoi et donne les lignes à coller", () => {
  offer = { current: "v0.0.2", target: "v0.0.3", arch: "amd64", available: false, reason: "rollback_unavailable" };
  const markup = render({ agent: agent({ rollback_capable: false }) });
  expect(markup).toContain("machine.agent_update_reason_rollback_unavailable");
  expect(markup).toContain("machine.agent_migration_title");
  expect(markup).toContain("sudo install guard");
  expect(markup).toContain("machine.agent_rollback_capable_no");
  expect(markup).not.toContain("machine.agent_update(v0.0.3)");
  // Le retour arrière non plus : l'agent ne sait pas le confirmer.
  expect(markup).not.toContain("machine.agent_rollback<");
});

test("une machine hors ligne n'offre pas le retour arrière", () => {
  expect(render({ online: false })).not.toContain("machine.agent_rollback<");
});

test("la dernière mise à jour se lit en une phrase : en attente, revenue en arrière, en échec", () => {
  runs = [run({ state: "accepted" })];
  expect(render()).toContain("machine.agent_update_state_pending");

  runs = [run({ state: "failed", reason: "rolled_back_crash" })];
  const rolledBack = render();
  expect(rolledBack).toContain("machine.agent_update_state_rolled_back(v0.0.3|machine.agent_update_cause_crash)");

  runs = [run({ state: "failed", reason: "rollback_failed" })];
  expect(render()).toContain("machine.agent_update_state_rollback_failed");

  runs = [run({ state: "succeeded" })];
  expect(render()).toContain("machine.agent_update_state_confirmed");
});

test("le drapeau « en dernier » se lit sur le bouton et se dit en une phrase", () => {
  expect(render({ updateLast: true })).toContain('aria-pressed="true"');
  expect(render({ updateLast: false })).toContain('aria-pressed="false"');
  expect(render()).toContain("machine.agent_update_last_text");
});
