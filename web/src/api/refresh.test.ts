import { describe, expect, it } from "vitest";

import { countsPath } from "./counts";
import { bumpVersions, initialVersions, topics, topicsOf, versionOf } from "./refresh";

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

  it("rereads a deployment plan when the directory, the orders or the machines change", () => {
    expect(topicsOf("/api/directory/services/svc_1/deploy-plan")).toEqual(["directory", "actions", "machines"]);
    expect(topicsOf("/api/directory/services/svc_1")).toEqual(["directory"]);
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

  it("rereads the SSH keys with the machines", () => {
    expect(topicsOf("/api/ssh-keys")).toEqual(["machines"]);
    expect(topicsOf("/api/machines/m1/ssh-keys")).toEqual(["machines"]);
  });

  it("rereads the orders of a machine on their own topic", () => {
    expect(topicsOf("/api/actions?machine=m1&limit=10")).toEqual(["actions"]);
  });

  it("rereads the update offer of an agent when its machine or its orders change", () => {
    expect(topicsOf("/api/machines/m1/agent-update")).toEqual(["machines", "actions"]);
  });

  it("rereads the audit forwarding state when a channel changes, the journal itself on demand only", () => {
    expect(topicsOf("/api/audit/forwarding")).toEqual(["alerts"]);
    expect(topicsOf("/api/audit?before=40")).toEqual([]);
  });

  it("rereads the backups when either their own topic or the orders change", () => {
    expect(topicsOf("/api/backups")).toEqual(["backups", "actions"]);
  });

  it("keeps the observed containers on the containers topic", () => {
    expect(topicsOf("/api/machines/m1/containers")).toEqual(["containers"]);
  });

  it("gives no topic to an unknown path, nor to the operator's access", () => {
    expect(topicsOf("/api/unknown")).toEqual([]);
    expect(topicsOf("/api/auth")).toEqual([]);
    expect(topicsOf("/api/settings/api-tokens")).toEqual([]);
    expect(topicsOf("/api/settings/disk")).toEqual([]);
  });

  it("rereads the end-of-support card on the containers topic, where its reading publishes", () => {
    expect(topicsOf("/api/settings/lifecycle")).toEqual(["containers"]);
  });

  it("rereads the counts on every counted topic, never on the measurements", () => {
    expect(topicsOf(countsPath)).toContain("machines");
    expect(topicsOf(countsPath)).toContain("status");
    expect(topicsOf(countsPath)).not.toContain("resources");
  });
});

describe("topics", () => {
  it("liste chaque sujet du direct une seule fois", () => {
    expect(new Set(topics).size).toBe(topics.length);
  });

});

describe("versionOf", () => {
  // La barre latérale et la vue d'ensemble : les compteurs bougent avec une
  // machine, pas avec un lot de mesures.
  it("moves the counts on machines, not on resources", () => {
    const start = versionOf(initialVersions(), 0, countsPath);
    expect(versionOf(bumpVersions(initialVersions(), "resources"), 0, countsPath)).toBe(start);
    expect(versionOf(bumpVersions(initialVersions(), "machines"), 0, countsPath)).not.toBe(start);
  });

  it("moves a path without topic only when everything is reread", () => {
    const touched = bumpVersions(bumpVersions(initialVersions(), "machines"), "resources");
    expect(versionOf(touched, 0, "/api/auth")).toBe(0);
    expect(versionOf(bumpVersions(initialVersions()), 1, "/api/auth")).toBe(1);
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
