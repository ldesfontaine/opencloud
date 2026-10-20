import { describe, expect, it } from "vitest";

import { ApiError } from "./client";
import { gapsPath, linksOfRefusal, unignorePath } from "./directory";

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

describe("linksOfRefusal", () => {
  const links = { probes: [{ id: "p1", name: "site" }], components: [] };

  it("lit la liste du corps d'un 409", () => {
    expect(linksOfRefusal(new ApiError(409, "directory.service_linked", { error: "directory.service_linked", links }))).toEqual(links);
  });

  it("ne dit rien d'un autre refus ni d'une autre erreur", () => {
    expect(linksOfRefusal(new ApiError(422, "directory.name_invalid", { error: "directory.name_invalid" }))).toBeNull();
    expect(linksOfRefusal(new ApiError(409, "directory.name_taken", { error: "directory.name_taken" }))).toBeNull();
    expect(linksOfRefusal(new Error("x"))).toBeNull();
  });
});
