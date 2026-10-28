import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router";
import { expect, test, vi } from "vitest";

import type { AuditEvent } from "../api/audit";
import { Audit } from "./Audit";

// Sans serveur, la traduction rend la clé et ses arguments : le test lit ce que la page demande.
vi.mock("../i18n/context", () => ({
  useT: () => (key: string, ...args: (string | number)[]) => (args.length === 0 ? key : `${key}(${args.join("|")})`),
  useI18n: () => ({ language: "fr" }),
}));

let events: AuditEvent[] = [];

vi.mock("../api/audit", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/audit")>()),
  useAudit: () => ({ data: { events, next_before: null }, error: null, reload: () => {} }),
  useAuditForwarding: () => ({ data: { channels: 1, failing: 0 }, error: null, reload: () => {} }),
}));

function event(over: Partial<AuditEvent>): AuditEvent {
  return {
    id: 1,
    at: "2026-10-28T09:00:00Z",
    actor_kind: "operator",
    actor_name: "",
    actor_label: null,
    action: "probe.pause",
    object_kind: "probe",
    object_id: "p1",
    object_name: "site",
    details: {},
    apply_id: null,
    ...over,
  };
}

function render(): string {
  return renderToStaticMarkup(
    <MemoryRouter>
      <Audit />
    </MemoryRouter>,
  );
}

test("un jeton se lit par son nom puis « pour : … » en évidence ; une personne n'en a pas", () => {
  events = [
    event({ id: 2, actor_kind: "api", actor_name: "tok1", actor_label: "agent-veille", details: { on_behalf_of: "alice" } }),
    event({ id: 1, actor_kind: "operator", actor_name: "lucas" }),
  ];
  const markup = render();
  expect(markup).toContain("audit.actor_api");
  expect(markup).toContain("agent-veille");
  expect(markup).toContain('<strong class="audit-for-whom">audit.on_behalf_of(alice)</strong>');
  expect(markup).toContain("audit.actor_operator");
  expect(markup).not.toContain("tok1");
  expect(markup.match(/audit-for-whom/g)).toHaveLength(1);
});

test("un jeton sans « pour qui » le dit, discrètement", () => {
  events = [event({ actor_kind: "api", actor_name: "tok1", actor_label: "agent-veille" })];
  const markup = render();
  expect(markup).toContain('<span class="muted">audit.on_behalf_of_unknown</span>');
  expect(markup).not.toContain("audit-for-whom");
});

test("une ligne d'avant le libellé montre l'identifiant du jeton", () => {
  events = [event({ actor_kind: "api", actor_name: "tok1", details: { on_behalf_of: "alice" } })];
  expect(render()).toContain("tok1");
});

test("le filtre par genre et la recherche sont proposés", () => {
  events = [event({})];
  const markup = render();
  expect(markup).toContain('aria-label="audit.filter_actor_kind"');
  expect(markup).toContain('type="search"');
  expect(markup).toContain('placeholder="audit.filter_actor_search"');
});
