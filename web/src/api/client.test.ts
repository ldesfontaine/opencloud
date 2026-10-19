import { afterEach, expect, test, vi } from "vitest";

import { api, ApiError, segment } from "./client";

afterEach(() => {
  vi.unstubAllGlobals();
});

function recordFetch(): RequestInit[] {
  const calls: RequestInit[] = [];
  vi.stubGlobal("fetch", (_path: string, init: RequestInit) => {
    calls.push(init);
    return Promise.resolve(new Response(null, { status: 204 }));
  });
  return calls;
}

test("une écriture sans corps part en JSON avec {}", async () => {
  const calls = recordFetch();
  await api.post("/api/mcp/actions/enable");
  await api.delete("/api/jobs/x");
  for (const init of calls) {
    expect(init.headers).toEqual({ "Content-Type": "application/json" });
    expect(init.body).toBe("{}");
  }
});

test("une écriture avec corps l'envoie tel quel, une lecture n'envoie rien", async () => {
  const calls = recordFetch();
  await api.put("/api/session/language", { language: "en" });
  await api.get("/api/session");
  expect(calls).toHaveLength(2);
  expect(calls[0]?.body).toBe('{"language":"en"}');
  expect(calls[1]?.headers).toBeUndefined();
  expect(calls[1]?.body).toBeUndefined();
});

test("un identifiant du serveur passe tel quel, encodé", () => {
  const ids = [
    "9f86d081884c7d65", // sonde, tâche, composant, incident
    "3f2b8c1e-5a4d-4e7b-9c1a-0d2e4f6a8b1c", // machine
    "local",
    "svc_9f86d081884c7d65",
    "unit_9f86d081884c7d65",
    "prj_9f86d081884c7d65",
    "apl_9f86d081884c7d65",
    "42", // alerte
    `local:${"a".repeat(64)}`, // conteneur : machine et identifiant Docker
  ];
  for (const id of ids) {
    expect(decodeURIComponent(segment(id))).toBe(id);
    expect(new URL(`/api/jobs/${segment(id)}`, "http://openCloud.test").pathname.split("/")).toHaveLength(4);
  }
});

test("un identifiant qui change le chemin lève la même erreur qu'un 404", () => {
  const forged = [
    "../x",
    "..",
    ".",
    "../../status/components/9f86d081884c7d65",
    "a/b",
    "a\\b",
    "a?b=c",
    "a#b",
    "%2e%2e",
    "%2F",
    "..%2F..%2Fstatus",
    "a b",
    "",
    "a".repeat(129),
  ];
  for (const id of forged) {
    expect(() => segment(id), JSON.stringify(id)).toThrow(ApiError);
    try {
      segment(id);
    } catch (error) {
      expect(error).toMatchObject({ status: 404, code: "not_found" });
    }
  }
});

test("un identifiant forgé ne sort jamais de sa collection", () => {
  // Ce que react-router rend d'un lien « /alertes/canaux/..%2F..%2Fstatus%2Fcomponents%2Fx ».
  const decoded = "../../status/components/x";
  const reached = (path: string) => new URL(path, "http://openCloud.test").pathname;
  expect(reached(`/api/alerts/channels/${decoded}`)).toBe("/api/status/components/x");
  expect(() => `/api/alerts/channels/${segment(decoded)}`).toThrow(ApiError);
});
