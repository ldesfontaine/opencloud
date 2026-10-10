import { afterEach, expect, test, vi } from "vitest";

import { api } from "./client";

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
