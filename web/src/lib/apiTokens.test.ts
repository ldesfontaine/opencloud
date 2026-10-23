import { expect, test } from "vitest";

import type { Translate } from "../i18n/context";
import { emptyTokenForm, envBlock, expiryLabel, lifetimeLabel, tokenRequestOf } from "./apiTokens";

const french: Record<string, string> = {
  "api_token.lifetime_days": "%d jours",
  "api_token.lifetime_never": "Sans échéance",
  "api_token.expires_never": "jamais",
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

test("un nouveau jeton part à l'échéance proposée", () => {
  expect(emptyTokenForm(90)).toEqual({ name: "", days: 90 });
});

test("tokenRequestOf nomme l'échéance comme l'API", () => {
  expect(tokenRequestOf({ name: "ci", days: 0 })).toEqual({ name: "ci", expires_in_days: 0 });
});

test("envBlock écrit les trois lignes du .env de mcp-maison", () => {
  const token = ["ock", "exemple"].join("_");
  expect(envBlock({ mcp_host: "127.0.0.1", opencloud_url: "http://127.0.0.1:8080" }, token)).toBe(
    `MCP_HOST=127.0.0.1\nOPENCLOUD_URL=http://127.0.0.1:8080\nOPENCLOUD_TOKEN=${token}`,
  );
});
