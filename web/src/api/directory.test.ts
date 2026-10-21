import { describe, expect, it } from "vitest";

import { ApiError } from "./client";
import { gapsPath, unignorePath } from "./directory";

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

describe("unignorePath", () => {
  it("met la clé d'observation en requête, encodée", () => {
    expect(unignorePath({ machine_id: "m1", kind: "container_name", value: "a b" })).toBe("/api/directory/ignored?machine=m1&kind=container_name&value=a+b");
  });
});
