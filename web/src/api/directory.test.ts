import { afterEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "./client";
import { gapsPath, unignoreObservation } from "./directory";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("gapsPath", () => {
  it("ne porte que les filtres choisis", () => {
    expect(gapsPath("", "")).toBe("/api/directory/gaps");
    expect(gapsPath("m1", "")).toBe("/api/directory/gaps?machine=m1");
    expect(gapsPath("m1", "prj_1")).toBe("/api/directory/gaps?machine=m1&project=prj_1");
  });

  it("refuse un filtre qui n'a pas la forme d'un identifiant", () => {
    expect(() => gapsPath("../x", "")).toThrow(ApiError);
  });
});

describe("unignoreObservation", () => {
  it("met la clé d'observation en requête, encodée", async () => {
    const sent: { path: string; method: string | undefined }[] = [];
    vi.stubGlobal("fetch", (path: string, init: RequestInit) => {
      sent.push({ path, method: init.method });
      return Promise.resolve(new Response(null, { status: 204 }));
    });
    await unignoreObservation({ machine_id: "m1", kind: "container_name", value: "a b" });
    expect(sent).toEqual([{ path: "/api/directory/ignored?machine=m1&kind=container_name&value=a+b", method: "DELETE" }]);
  });
});
