import { expect, test } from "vitest";

import type { Translate } from "../i18n/context";
import { claudeCodeCommand, emptyTokenForm, expiryLabel, lifetimeLabel, lifetimesOf, mcpScopes, redirectFormOf, redirectLines, redirectRequestOf, tokenRequestOf } from "./mcp";

const french: Record<string, string> = {
  "time.hours": "%d h",
  "time.days": "%d j",
  "mcp.lifetimes": "Un accès vaut %s, renouvelé jusqu'à %s sans nouvelle autorisation.",
  "mcp.lifetime_days": "%d jours",
  "mcp.lifetime_never": "Sans échéance",
  "mcp.expires_never": "jamais",
};

const t: Translate = (key, ...args) => {
  let text = french[key] ?? key;
  for (const arg of args) {
    text = text.replace(/%[sd]/, String(arg));
  }
  return text;
};

test("redirectLines garde une adresse par ligne, sans les vides", () => {
  expect(redirectLines("  https://claude.ai/api/mcp/auth_callback \n\n\nhttps://b.example/cb\n")).toEqual([
    "https://claude.ai/api/mcp/auth_callback",
    "https://b.example/cb",
  ]);
  expect(redirectLines("")).toEqual([]);
});

test("lifetimesOf dit les durées du serveur en mots", () => {
  expect(lifetimesOf(t, 3600, 30 * 24 * 3600)).toBe("Un accès vaut 1 h, renouvelé jusqu'à 30 j sans nouvelle autorisation.");
});

test("claudeCodeCommand enveloppe la commande du client local", () => {
  expect(claudeCodeCommand("opencloud", "opencloud mcp -config /etc/opencloud/config.toml")).toBe(
    "claude mcp add opencloud -- opencloud mcp -config /etc/opencloud/config.toml",
  );
});

test("lifetimeLabel dit une durée en jours, ou sans échéance pour 0", () => {
  expect(lifetimeLabel(t, 90)).toBe("90 jours");
  expect(lifetimeLabel(t, 0)).toBe("Sans échéance");
});

test("expiryLabel dit la date, ou jamais pour un jeton sans échéance", () => {
  expect(expiryLabel(t, null, () => "x")).toBe("jamais");
  expect(expiryLabel(t, "2026-12-22T10:00:00Z", (iso) => `le ${iso}`)).toBe("le 2026-12-22T10:00:00Z");
});

test("la lecture seule est la première portée, celle par défaut", () => {
  expect(mcpScopes).toEqual(["read", "write"]);
});

test("redirectFormOf montre une adresse par ligne, redirectRequestOf renvoie la liste nettoyée", () => {
  const form = redirectFormOf({ redirect_uris: ["https://a.exemple.fr/cb", "http://localhost:3000/cb"] });
  expect(form.uris).toBe("https://a.exemple.fr/cb\nhttp://localhost:3000/cb");
  expect(redirectRequestOf({ uris: `${form.uris}\n\n  https://b.exemple.fr/cb  ` })).toEqual(["https://a.exemple.fr/cb", "http://localhost:3000/cb", "https://b.exemple.fr/cb"]);
});

test("un nouveau jeton part en lecture seule, à l'échéance proposée", () => {
  expect(emptyTokenForm(90)).toEqual({ name: "", scope: "read", days: 90 });
});

test("tokenRequestOf nomme l'échéance comme l'API", () => {
  expect(tokenRequestOf({ name: "ci", scope: "write", days: 0 })).toEqual({ name: "ci", scope: "write", expires_in_days: 0 });
});
