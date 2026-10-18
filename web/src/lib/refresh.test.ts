import { describe, expect, it } from "vitest";

import { topicsOf } from "./refresh";

describe("topicsOf", () => {
  it("sends the declared directory to its own topic, though its path says services", () => {
    expect(topicsOf("/api/directory/services")).toEqual(["directory"]);
    expect(topicsOf("/api/directory/services/svc_1")).toEqual(["directory"]);
    expect(topicsOf("/api/directory/projects")).toEqual(["directory"]);
  });

  it("rereads the gap when either the directory or the containers change", () => {
    expect(topicsOf("/api/directory/gaps")).toEqual(["directory", "containers"]);
    expect(topicsOf("/api/directory/gaps?machine=m1&project=prj_1")).toEqual(["directory", "containers"]);
  });

  it("rereads an adoption proposal only when the directory changes", () => {
    expect(topicsOf("/api/directory/proposal?container=abc")).toEqual(["directory"]);
  });

  it("rereads a container sheet when the directory changes, but not its sub-paths or the list", () => {
    expect(topicsOf("/api/containers/x")).toEqual(["containers", "directory"]);
    expect(topicsOf("/api/containers/x?lang=fr")).toEqual(["containers", "directory"]);
    expect(topicsOf("/api/containers/x/resources")).toEqual(["resources"]);
    expect(topicsOf("/api/containers")).toEqual(["containers"]);
  });

  it("keeps the observed containers on the containers topic", () => {
    expect(topicsOf("/api/machines/m1/containers")).toEqual(["containers"]);
  });

  it("refreshes every topic for an unknown path", () => {
    expect(topicsOf("/api/unknown")).toContain("directory");
  });
});
