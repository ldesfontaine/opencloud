import { describe, expect, it } from "vitest";

import { topicsOf } from "./refresh";

describe("topicsOf", () => {
  it("sends the declared directory to its own topic, though its path says services", () => {
    expect(topicsOf("/api/directory/services")).toEqual(["directory"]);
    expect(topicsOf("/api/directory/services/svc_1")).toEqual(["directory"]);
    expect(topicsOf("/api/directory/projects")).toEqual(["directory"]);
  });

  it("keeps the observed containers on the services topic", () => {
    expect(topicsOf("/api/services/x")).toEqual(["services"]);
    expect(topicsOf("/api/machines/m1/services")).toEqual(["services"]);
  });

  it("refreshes every topic for an unknown path", () => {
    expect(topicsOf("/api/unknown")).toContain("directory");
  });
});
