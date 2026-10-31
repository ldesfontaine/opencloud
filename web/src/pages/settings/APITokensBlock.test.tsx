import { renderToStaticMarkup } from "react-dom/server";
import { expect, test, vi } from "vitest";

import type { APIToken, APITokensState, VerbOffer } from "../../api/apiTokens";
import { APITokensBlock } from "./APITokensBlock";
import { emptyReachForm } from "../../lib/apiTokens";
import { ReachChoices } from "./ReachChoices";
import { VerbChoices } from "./VerbChoices";

// Sans serveur, la traduction rend la clé et ses arguments : le test lit ce que la page demande.
vi.mock("../../i18n/context", () => ({
  useT: () => (key: string, ...args: (string | number)[]) => (args.length === 0 ? key : `${key}(${args.join("|")})`),
  useI18n: () => ({ language: "fr" }),
}));

const offers: VerbOffer[] = [
  { id: "probe.pause", label: "api_token.verb_probe_pause", summary: "api_token.verb_probe_pause_text", reverse: "probe.resume" },
  { id: "probe.resume", label: "api_token.verb_probe_resume", summary: "api_token.verb_probe_resume_text", reverse: "probe.pause" },
];

function token(over: Partial<APIToken>): APIToken {
  return { id: "t1", name: "agent", masked: "ock_ab…", scope: "read", verbs: [], machines: null, guests: null, created_at: "2026-10-28T10:00:00Z", expires_at: null, last_used_at: null, ...over };
}

function state(tokens: APIToken[]): APITokensState {
  return {
    tokens,
    machines: [
      { id: "m1", name: "vps-1" },
      { id: "m2", name: "vps-2" },
    ],
    limit: 16,
    lifetimes: { days: [30, 90, 365, 0], default_days: 90 },
    verbs: offers,
    env: { mcp_host: "127.0.0.1", opencloud_url: "http://127.0.0.1:8080" },
  };
}

test("chaque verbe se coche avec son libellé, sa phrase et son inverse, et rien n'est coché d'avance", () => {
  const markup = renderToStaticMarkup(<VerbChoices offers={offers} chosen={[]} onToggle={() => {}} />);
  expect(markup).toContain("api_token.verb_probe_pause_text");
  expect(markup).toContain("api_token.verb_reverse(api_token.verb_probe_resume)");
  expect(markup).toContain("api_token.verbs_none");
  expect(markup).not.toContain("checked");
});

test("un verbe coché est montré coché, et l'avertissement de l'absence de verbe disparaît", () => {
  const markup = renderToStaticMarkup(<VerbChoices offers={offers} chosen={["probe.pause"]} onToggle={() => {}} />);
  expect(markup.match(/checked/g)).toHaveLength(1);
  expect(markup).not.toContain("api_token.verbs_none");
});

test("la liste montre la portée de chaque jeton et les libellés de ses verbes", () => {
  const markup = renderToStaticMarkup(
    <APITokensBlock
      state={state([token({ id: "r", name: "lecteur" }), token({ id: "w", name: "agent", scope: "write", verbs: ["probe.pause"] }), token({ id: "m", name: "muet", scope: "write" })])}
      reload={() => {}}
    />,
  );
  expect(markup).toContain("api_token.scope_read");
  expect(markup).toContain("api_token.scope_write : api_token.verb_probe_pause");
  expect(markup).toContain("api_token.scope_write : api_token.verbs_none_list");
});

test("le formulaire part en lecture : ni verbes ni avertissement d'écriture avant le choix", () => {
  const markup = renderToStaticMarkup(<APITokensBlock state={state([])} reload={() => {}} />);
  expect(markup).toContain("api_token.scope_label");
  expect(markup).not.toContain("api_token.write_warn");
  expect(markup).not.toContain("api_token.verb_probe_pause");
});

test("la liste montre la portée machines d'un jeton limité, et propose de la modifier à l'écriture seule", () => {
  const markup = renderToStaticMarkup(
    <APITokensBlock
      state={state([token({ id: "r", name: "lecteur" }), token({ id: "w", name: "agent", scope: "write", verbs: ["probe.pause"], machines: ["m2"] })])}
      reload={() => {}}
    />,
  );
  expect(markup).toContain("api_token.reach_machines_value(vps-2)");
  expect(markup.match(/api_token\.reach_edit/g)).toHaveLength(1);
});

test("le formulaire en écriture offre toutes/certaines machines, sans choix d'invités avant un verbe Proxmox", () => {
  const markup = renderToStaticMarkup(<ReachChoices machines={state([]).machines} values={emptyReachForm()} withGuests={false} idPrefix="x" onChange={() => {}} />);
  expect(markup).toContain("api_token.reach_machines_all");
  expect(markup).not.toContain("api_token.reach_guests_all");
  expect(markup).not.toContain("vps-1");
});

test("« certaines machines » liste les machines à cocher, et dit qu'aucune cochée n'agit sur rien", () => {
  const values = { ...emptyReachForm(), machinesMode: "some" as const, machines: ["m1"] };
  const markup = renderToStaticMarkup(<ReachChoices machines={state([]).machines} values={values} withGuests={true} idPrefix="x" onChange={() => {}} />);
  expect(markup).toContain("vps-1");
  expect(markup).toContain("vps-2");
  expect(markup.match(/checked/g)).toHaveLength(1);
  expect(markup).toContain("api_token.reach_guests_all");
  const none = renderToStaticMarkup(<ReachChoices machines={state([]).machines} values={{ ...values, machines: [] }} withGuests={false} idPrefix="x" onChange={() => {}} />);
  expect(none).toContain("api_token.reach_machines_none");
});

test("des invités limités montrent le champ des numéros, et un numéro invalide est dit", () => {
  const values = { ...emptyReachForm(), guestsMode: "some" as const, guests: "200 abc" };
  const markup = renderToStaticMarkup(<ReachChoices machines={[]} values={values} withGuests={true} idPrefix="x" onChange={() => {}} />);
  expect(markup).toContain("api_token.reach_guests_label");
  expect(markup).toContain("api_token.reach_guests_invalid");
});
