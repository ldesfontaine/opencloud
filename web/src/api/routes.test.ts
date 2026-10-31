import { describe, expect, it } from "vitest";

import { ApiError } from "./client";
import { route } from "./routes";

describe("route", () => {
  it("rend le verbe et le chemin d'un motif sans segment", () => {
    expect(route("POST /api/probes")).toEqual({ method: "POST", path: "/api/probes", pattern: "POST /api/probes" });
  });

  it("remplit les segments, chacun gardé par segment()", () => {
    expect(route("POST /api/machines/{id}/proxmox/guests/{vmid}/actions/start", { id: "m1", vmid: "104" }).path).toBe(
      "/api/machines/m1/proxmox/guests/104/actions/start",
    );
    expect(() => route("DELETE /api/probes/{id}", { id: "../x" })).toThrow(ApiError);
  });

  it("ajoute la requête de filtre après le chemin", () => {
    const query = new URLSearchParams({ value: "a b" });
    expect(route("DELETE /api/directory/ignored", query).path).toBe("/api/directory/ignored?value=a+b");
  });
});
