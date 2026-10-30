import { expect, test } from "vitest";

import type { Translate } from "../i18n/context";
import type { APIToken } from "../api/apiTokens";
import { emptyTokenForm, envBlock, expiryLabel, lifetimeLabel, scopeLabel, toggleVerb, tokenRequestOf } from "./apiTokens";

const french: Record<string, string> = {
  "api_token.lifetime_days": "%d jours",
  "api_token.lifetime_never": "Sans échéance",
  "api_token.expires_never": "jamais",
  "api_token.scope_read": "Lecture",
  "api_token.scope_write": "Écriture",
  "api_token.verbs_none_list": "aucun verbe",
};

const t: Translate = (key, ...args) => {
  let text = french[key] ?? key;
  for (const arg of args) {
    text = text.replace(/%[sd]/, String(arg));
  }
  return text;
};

test("lifetimeLabel dit une durée en jours, ou sans échéance pour 0", () => {
  expect(lifetimeLabel(t, 90)).toBe("90 jours");
  expect(lifetimeLabel(t, 0)).toBe("Sans échéance");
});

test("expiryLabel dit la date, ou jamais pour un jeton sans échéance", () => {
  expect(expiryLabel(t, null, () => "x")).toBe("jamais");
  expect(expiryLabel(t, "2026-12-22T10:00:00Z", (iso) => `le ${iso}`)).toBe("le 2026-12-22T10:00:00Z");
});

test("un nouveau jeton part à l'échéance proposée, en lecture, sans verbe coché", () => {
  expect(emptyTokenForm(90)).toEqual({ name: "", days: 90, scope: "read", verbs: [] });
});

test("tokenRequestOf nomme l'échéance et la portée comme l'API", () => {
  expect(tokenRequestOf({ name: "ci", days: 0, scope: "read", verbs: [] })).toEqual({ name: "ci", expires_in_days: 0, scope: "read" });
});

test("tokenRequestOf n'envoie les verbes qu'avec l'écriture", () => {
  expect(tokenRequestOf({ name: "ci", days: 30, scope: "write", verbs: ["probe.pause"] })).toEqual({
    name: "ci",
    expires_in_days: 30,
    scope: "write",
    verbs: ["probe.pause"],
  });
  // Cochés puis repassés en lecture : le serveur refuserait la demande.
  expect(tokenRequestOf({ name: "ci", days: 30, scope: "read", verbs: ["probe.pause"] }).verbs).toBeUndefined();
});

test("toggleVerb coche puis décoche", () => {
  expect(toggleVerb([], "probe.pause")).toEqual(["probe.pause"]);
  expect(toggleVerb(["probe.pause", "job.pause"], "probe.pause")).toEqual(["job.pause"]);
});

const token = (patch: Partial<APIToken>): APIToken => ({
  id: "t",
  name: "n",
  masked: "ock_…",
  scope: "read",
  verbs: [],
  created_at: "2026-10-28T10:00:00Z",
  expires_at: null,
  last_used_at: null,
  ...patch,
});

test("scopeLabel dit la portée, et les verbes d'un jeton en écriture", () => {
  const labelOf = (id: string) => `<${id}>`;
  expect(scopeLabel(t, token({}), labelOf)).toBe("Lecture");
  expect(scopeLabel(t, token({ scope: "write", verbs: ["a.b", "c.d"] }), labelOf)).toBe("Écriture : <a.b>, <c.d>");
  expect(scopeLabel(t, token({ scope: "write" }), labelOf)).toBe("Écriture : aucun verbe");
});

test("envBlock écrit les trois lignes du .env de mcp-maison", () => {
  const token = ["ock", "exemple"].join("_");
  expect(envBlock({ mcp_host: "127.0.0.1", opencloud_url: "http://127.0.0.1:8080" }, token)).toBe(
    `MCP_HOST=127.0.0.1\nOPENCLOUD_URL=http://127.0.0.1:8080\nOPENCLOUD_TOKEN=${token}`,
  );
});
