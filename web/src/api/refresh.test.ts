import { describe, expect, it } from "vitest";

import { bumpVersions, initialVersions, topics, topicsOf } from "./refresh";

describe("topicsOf", () => {
  it("sends the declared directory to its own topic, though its path says services", () => {
    expect(topicsOf("/api/directory/services")).toEqual(["directory"]);
    expect(topicsOf("/api/directory/services/svc_1")).toEqual(["directory"]);
    expect(topicsOf("/api/directory/projects")).toEqual(["directory"]);
  });

  it("rereads the file journal when the directory changes", () => {
    expect(topicsOf("/api/manifest/applies")).toEqual(["directory"]);
    expect(topicsOf("/api/manifest/applies/apl_0123456789abcdef")).toEqual(["directory"]);
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

describe("topics", () => {
  it("liste chaque sujet du direct une seule fois", () => {
    expect(new Set(topics).size).toBe(topics.length);
  });

  it("relit tout sauf les mesures pour un chemin inconnu", () => {
    expect(topicsOf("/api/unknown")).toEqual(["machines", "jobs", "containers", "probes", "status", "alerts", "directory"]);
  });
});

describe("bumpVersions", () => {
  it("part de zéro pour chaque sujet", () => {
    const versions = initialVersions();
    expect(Object.keys(versions)).toEqual([...topics]);
    expect(Object.values(versions).every((version) => version === 0)).toBe(true);
  });

  it("fait avancer le seul sujet demandé", () => {
    const next = bumpVersions(initialVersions(), "jobs");
    expect(next.jobs).toBe(1);
    expect(topics.filter((topic) => next[topic] !== 0)).toEqual(["jobs"]);
  });

  it("fait avancer tous les sujets sans argument, sans toucher à l'original", () => {
    const before = initialVersions();
    const next = bumpVersions(bumpVersions(before, "alerts"));
    expect(next.alerts).toBe(2);
    expect(next.machines).toBe(1);
    expect(topics.every((topic) => before[topic] === 0)).toBe(true);
  });
});
