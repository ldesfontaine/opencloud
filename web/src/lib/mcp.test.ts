import { expect, test } from "vitest";

import type { Translate } from "../i18n/context";
import { claudeCodeCommand, lifetimesOf, redirectLines } from "./mcp";

const french: Record<string, string> = {
  "time.hours": "%d h",
  "time.days": "%d j",
  "mcp.lifetimes": "Un accès vaut %s, renouvelé jusqu'à %s sans nouvelle autorisation.",
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
  expect(claudeCodeCommand("opencloud mcp -config /etc/opencloud/config.toml")).toBe(
    "claude mcp add opencloud -- opencloud mcp -config /etc/opencloud/config.toml",
  );
});
