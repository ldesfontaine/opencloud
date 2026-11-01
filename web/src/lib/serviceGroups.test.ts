import { describe, expect, it } from "vitest";

import type { DeclaredService, ServiceGap } from "../api/directory";
import { groupServices, worstGap } from "./serviceGroups";

function service(id: string, name: string, machine: string, project: string, image = name): DeclaredService {
  return {
    id,
    name,
    machine_name: machine,
    compose_project: project,
    units: [{ name, image }],
  } as unknown as DeclaredService;
}

describe("groupServices", () => {
  it("met chaque service Compose sur sa ligne, sous le projet Compose qui les regroupe", () => {
    const sections = groupServices([
      service("1", "traefik", "vm-edge", "edge"),
      service("2", "authelia", "vm-edge", "edge"),
      service("3", "crowdsec", "vm-edge", "edge"),
    ]);
    expect(sections.map((section) => section.project)).toEqual(["edge"]);
    expect(sections[0]?.groups.map((group) => group.name)).toEqual(["authelia", "crowdsec", "traefik"]);
  });

  it("regroupe le même nom et la même image sur deux machines en deux instances", () => {
    const sections = groupServices([service("1", "traefik", "vm-b", "edge"), service("2", "traefik", "vm-a", "edge"), service("3", "authelia", "vm-a", "edge")]);
    const traefik = sections[0]?.groups.find((group) => group.name === "traefik");
    expect(traefik?.instances.map((instance) => instance.machine_name)).toEqual(["vm-a", "vm-b"]);
  });

  it("sépare deux services de même nom dont l'image diffère", () => {
    const sections = groupServices([service("1", "web", "vm-a", "blog", "nginx"), service("2", "web", "vm-b", "blog", "caddy")]);
    expect(sections[0]?.groups).toHaveLength(2);
  });

  it("range les services sans projet Compose en dernier", () => {
    const sections = groupServices([service("1", "whoami", "vm-a", ""), service("2", "traefik", "vm-a", "edge"), service("3", "app", "vm-a", "blog")]);
    expect(sections.map((section) => section.project)).toEqual(["blog", "edge", ""]);
  });
});

const gap = (state: ServiceGap["state"]) => ({ state }) as ServiceGap;

describe("worstGap", () => {
  const instances = [service("1", "t", "a", ""), service("2", "t", "b", "")];
  it("montre le pire écart des instances", () => {
    const gaps = new Map([["1", gap("conform")], ["2", gap("diverged")]]);
    expect(worstGap(instances, gaps)?.state).toBe("diverged");
  });
  it("ne dit rien quand aucune instance n'a d'écart", () => {
    expect(worstGap(instances, new Map())).toBeUndefined();
  });
});
