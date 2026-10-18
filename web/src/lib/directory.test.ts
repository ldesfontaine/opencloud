import { describe, expect, it } from "vitest";

import type { DeclaredService } from "../api/types";
import { emptyServiceForm, formatPorts, parsePorts, serviceFormOf, serviceRequestOf, toggled, unitKeyOf } from "./directory";

function nextcloud(): DeclaredService {
  return {
    id: "svc_1",
    machine_id: "m1",
    machine_name: "vps-paris-1",
    name: "nextcloud",
    title: "Nextcloud",
    nature: "application",
    origin: "compose",
    compose_project: "nextcloud",
    public_url: "https://cloud.exemple.fr",
    declared_by: "operator",
    needs_review: false,
    provides: [],
    units: [
      { id: "unit_1", name: "app", kind: "container", image: "nextcloud", version: "29.0.4", digest: "", container_name: "", systemd_unit: "", ports: ["8080:80", "443/tcp"] },
      { id: "unit_2", name: "db", kind: "container", image: "postgres", version: "16.4", digest: "", container_name: "", systemd_unit: "", ports: [] },
    ],
    project_ids: ["prj_1"],
    created_at: "2026-10-18T09:00:00Z",
    updated_at: "2026-10-18T09:00:00Z",
  };
}

describe("parsePorts", () => {
  it("sépare par des virgules ou des espaces et ignore le vide", () => {
    expect(parsePorts("80, 8080:80  443/tcp,,")).toEqual(["80", "8080:80", "443/tcp"]);
    expect(parsePorts("   ")).toEqual([]);
  });

  it("se relit tel qu'il s'est écrit", () => {
    expect(parsePorts(formatPorts(["8080:80", "53/udp"]))).toEqual(["8080:80", "53/udp"]);
  });
});

describe("serviceRequestOf", () => {
  it("renvoie au serveur ce qu'il a donné, identifiants d'unités compris", () => {
    const service = nextcloud();
    const request = serviceRequestOf(serviceFormOf(service));
    expect(request).toEqual({
      machine_id: "m1",
      name: "nextcloud",
      title: "Nextcloud",
      nature: "application",
      origin: "compose",
      compose_project: "nextcloud",
      public_url: "https://cloud.exemple.fr",
      provides: [],
      units: [
        { id: "unit_1", name: "app", image: "nextcloud", version: "29.0.4", digest: "", container_name: "", systemd_unit: "", ports: ["8080:80", "443/tcp"] },
        { id: "unit_2", name: "db", image: "postgres", version: "16.4", digest: "", container_name: "", systemd_unit: "", ports: [] },
      ],
      project_ids: ["prj_1"],
    });
  });

  it("vide les champs sans objet pour l'origine", () => {
    const form = serviceFormOf(nextcloud());
    form.units[0] = { ...form.units[0]!, containerName: "nextcloud-app", systemdUnit: "nextcloud.service", digest: `sha256:${"a".repeat(64)}` };
    form.provides = ["proxy"];

    const compose = serviceRequestOf(form);
    expect(compose.units[0]).toMatchObject({ container_name: "", systemd_unit: "", image: "nextcloud" });
    // Une application ne fournit rien, même si la case était cochée avant.
    expect(compose.provides).toEqual([]);

    const container = serviceRequestOf({ ...form, origin: "container" });
    expect(container.compose_project).toBe("");
    expect(container.units[0]).toMatchObject({ container_name: "nextcloud-app", systemd_unit: "", image: "nextcloud", digest: `sha256:${"a".repeat(64)}` });

    const binary = serviceRequestOf({ ...form, origin: "binary", nature: "infra" });
    expect(binary.units[0]).toMatchObject({ container_name: "", systemd_unit: "nextcloud.service", image: "", digest: "" });
    expect(binary.provides).toEqual(["proxy"]);
  });
});

describe("emptyServiceForm", () => {
  it("ne partage pas ses listes d'un formulaire à l'autre", () => {
    const first = emptyServiceForm();
    first.units.push({ ...first.units[0]!, name: "x" });
    first.provides.push("tls");
    const second = emptyServiceForm();
    expect(second.units).toHaveLength(1);
    expect(second.provides).toEqual([]);
  });
});

describe("unitKeyOf", () => {
  it("rend ce qui retrouve l'unité selon l'origine", () => {
    const unit = { ...nextcloud().units[0]!, container_name: "nextcloud-app", systemd_unit: "nextcloud.service" };
    expect(unitKeyOf(unit, "compose")).toBe("app");
    expect(unitKeyOf(unit, "container")).toBe("nextcloud-app");
    expect(unitKeyOf(unit, "binary")).toBe("nextcloud.service");
  });
});

describe("toggled", () => {
  it("ajoute ce qui manque et retire ce qui y est", () => {
    expect(toggled(["a"], "b")).toEqual(["a", "b"]);
    expect(toggled(["a", "b"], "a")).toEqual(["b"]);
  });
});
