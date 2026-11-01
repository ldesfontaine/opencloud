import { describe, expect, it } from "vitest";

import type { DeclaredService, Project, ProposalResponse } from "../api/directory";
import {
  acceptanceOf,
  emptyServiceForm,
  emptyUnitForm,
  formatUnitPorts,
  hasAdvancedUnitValues,
  hasLinks,
  parsePorts,
  projectNames,
  serviceFormOf,
  serviceFormOfDefinition,
  serviceFormStart,
  serviceRequestOf,
  toggled,
  unitKeyOf,
  unitSummary,
} from "./directory";

function nextcloud(): DeclaredService {
  return {
    id: "svc_1",
    machine_id: "m1",
    machine_name: "vps-paris-1",
    name: "nextcloud",
    title: "Nextcloud",
    nature: "application",
    category: "",
    category_deduced: "application",
    origin: "compose",
    compose_project: "nextcloud",
    public_url: "https://cloud.exemple.fr",
    declared_by: "operator",
    provides: [],
    units: [
      { id: "unit_1", name: "app", kind: "container", image: "nextcloud", version: "29.0.4", digest: "", container_name: "", systemd_unit: "", data_path: "/var/www/html", ports: ["8080:80", "443/tcp"], update_policy: "hold" },
      { id: "unit_2", name: "db", kind: "container", image: "postgres", version: "16.4", digest: "", container_name: "", systemd_unit: "", data_path: "", ports: [], update_policy: "watch" },
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
    expect(parsePorts(formatUnitPorts(["8080:80", "53/udp"]))).toEqual(["8080:80", "53/udp"]);
  });
});

describe("formatUnitPorts", () => {
  it("écrit les ports d'une unité sur une ligne, séparés par des virgules", () => {
    expect(formatUnitPorts(["80", "8080:80", "53/udp"])).toBe("80, 8080:80, 53/udp");
    expect(formatUnitPorts([])).toBe("");
  });
});

describe("projectNames", () => {
  it("donne le nom de chaque projet par son identifiant", () => {
    const projects = [
      { id: "prj_1", name: "cloud", title: "Cloud", service_ids: [], publication: null, created_at: "", updated_at: "" },
      { id: "prj_2", name: "mail", title: "Courrier", service_ids: [], publication: null, created_at: "", updated_at: "" },
    ] satisfies Project[];
    const names = projectNames(projects);
    expect(names.get("prj_1")).toBe("cloud");
    expect(names.get("prj_2")).toBe("mail");
    expect(names.get("prj_3")).toBeUndefined();
  });

  it("ne rend rien sans projet", () => {
    expect(projectNames([]).size).toBe(0);
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
      category: "",
      origin: "compose",
      compose_project: "nextcloud",
      public_url: "https://cloud.exemple.fr",
      provides: [],
      units: [
        { id: "unit_1", name: "app", image: "nextcloud", version: "29.0.4", digest: "", container_name: "", systemd_unit: "", data_path: "/var/www/html", ports: ["8080:80", "443/tcp"], update_policy: "hold" },
        { id: "unit_2", name: "db", image: "postgres", version: "16.4", digest: "", container_name: "", systemd_unit: "", data_path: "", ports: [], update_policy: "watch" },
      ],
      project_ids: ["prj_1"],
    });
  });

  it("vide les champs sans objet pour l'origine", () => {
    const form = serviceFormOf(nextcloud());
    form.units[0] = { ...form.units[0]!, containerName: "nextcloud-app", systemdUnit: "nextcloud.service", digest: `sha256:${"a".repeat(64)}` };
    form.provides = ["proxy"];

    const compose = serviceRequestOf(form);
    expect(compose.units[0]).toMatchObject({ container_name: "", systemd_unit: "", image: "nextcloud", data_path: "/var/www/html" });
    // Une application ne fournit rien, même si la case était cochée avant.
    expect(compose.provides).toEqual([]);

    const container = serviceRequestOf({ ...form, origin: "container" });
    expect(container.compose_project).toBe("");
    expect(container.units[0]).toMatchObject({ container_name: "nextcloud-app", systemd_unit: "", image: "nextcloud", digest: `sha256:${"a".repeat(64)}` });

    const binary = serviceRequestOf({ ...form, origin: "binary", nature: "infra" });
    expect(binary.units[0]).toMatchObject({ container_name: "", systemd_unit: "nextcloud.service", image: "", digest: "", data_path: "" });
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

describe("serviceFormOfDefinition", () => {
  const definition: ProposalResponse["definition"] = {
    machine_id: "m1",
    name: "nextcloud",
    title: "",
    nature: "application",
    category: "",
    origin: "compose",
    compose_project: "nextcloud",
    public_url: "",
    provides: [],
    units: [
      { id: "unit_1", name: "app", image: "nextcloud", version: "29.0.4", digest: "", container_name: "", systemd_unit: "", data_path: "", ports: ["8080:80"], update_policy: "hold" },
      // Une unité ajoutée par la proposition arrive sans identifiant.
      { id: "", name: "redis", image: "redis", version: "", digest: "", container_name: "", systemd_unit: "", data_path: "", ports: [] },
    ],
    project_ids: ["prj_1"],
  };

  it("reprend la proposition du serveur, ids d'unités compris", () => {
    const form = serviceFormOfDefinition(definition);
    expect(form.machineID).toBe("m1");
    expect(form.composeProject).toBe("nextcloud");
    expect(form.units.map((unit) => unit.id)).toEqual(["unit_1", ""]);
    expect(form.units[0]?.ports).toBe("8080:80");
    expect(form.projectIDs).toEqual(["prj_1"]);
  });

  it("renvoie au serveur la même définition que celle reçue", () => {
    const request = serviceRequestOf(serviceFormOfDefinition(definition));
    expect(request.units.map((unit) => ({ id: unit.id, name: unit.name, ports: unit.ports, update_policy: unit.update_policy }))).toEqual([
      { id: "unit_1", name: "app", ports: ["8080:80"], update_policy: "hold" },
      // Sans politique dite, une unité ajoutée se suit.
      { id: "", name: "redis", ports: [], update_policy: "watch" },
    ]);
    expect(request.compose_project).toBe("nextcloud");
  });

  it("ne partage pas ses listes avec la proposition", () => {
    const form = serviceFormOfDefinition(definition);
    form.projectIDs.push("prj_2");
    expect(definition.project_ids).toEqual(["prj_1"]);
  });
});

// Ce que le serveur propose pour un conteneur : un service neuf, d'une seule
// unité (le service Compose du conteneur).
function proposal(): ProposalResponse {
  return {
    definition: {
      machine_id: "m2",
      name: "redis",
      title: "",
      nature: "application",
      category: "",
      origin: "compose",
      compose_project: "nextcloud",
      public_url: "",
      provides: [],
      units: [{ id: "", name: "redis", image: "redis", version: "", digest: "", container_name: "", systemd_unit: "", data_path: "", ports: [] }],
      project_ids: [],
    },
  };
}

describe("serviceFormStart", () => {
  it("part du service gardé en édition", () => {
    expect(serviceFormStart(nextcloud(), null)?.machineID).toBe("m1");
    expect(serviceFormStart(nextcloud(), proposal())?.machineID).toBe("m1");
  });

  it("en création, part de la proposition, et attend qu'elle arrive", () => {
    expect(serviceFormStart(null, null)).toBeNull();
    expect(serviceFormStart(null, proposal())?.machineID).toBe("m2");
  });
});

describe("la politique de mise à jour d'une unité", () => {
  it("fait l'aller-retour entre le serveur et le formulaire", () => {
    const form = serviceFormOf(nextcloud());
    expect(form.units.map((unit) => unit.updatePolicy)).toEqual(["hold", "watch"]);
    form.units[1] = { ...form.units[1]!, updatePolicy: "ignore" };
    expect(serviceRequestOf(form).units.map((unit) => unit.update_policy)).toEqual(["hold", "ignore"]);
  });

  it("force watch pour un binaire, même si le formulaire gardait une autre valeur", () => {
    const form = serviceFormOf(nextcloud());
    const request = serviceRequestOf({ ...form, origin: "binary", nature: "infra" });
    expect(request.units.map((unit) => unit.update_policy)).toEqual(["watch", "watch"]);
  });
});

describe("hasLinks", () => {
  it("dit si quelque chose lie le service", () => {
    expect(hasLinks({ probes: [{ id: "p1", name: "site" }], components: [] })).toBe(true);
    expect(hasLinks({ probes: [], components: [] })).toBe(false);
  });
});

describe("unitSummary", () => {
  const unit = { ...emptyUnitForm(), name: "app", image: "nextcloud:29", containerName: "nc-app", systemdUnit: "nc.service", version: "29.0.4" };

  it("dit ce qui retrouve l'unité selon l'origine, puis l'image et la version", () => {
    expect(unitSummary(unit, "compose")).toBe("app · nextcloud:29 · 29.0.4");
    expect(unitSummary(unit, "container")).toBe("nc-app · nextcloud:29 · 29.0.4");
  });

  it("n'a pas d'image pour un binaire et saute ce qui est vide", () => {
    expect(unitSummary(unit, "binary")).toBe("nc.service · 29.0.4");
    expect(unitSummary({ ...unit, version: " " }, "binary")).toBe("nc.service");
  });
});

describe("hasAdvancedUnitValues", () => {
  it("est faux pour une unité neuve ou à peine nommée", () => {
    expect(hasAdvancedUnitValues(emptyUnitForm())).toBe(false);
    expect(hasAdvancedUnitValues({ ...emptyUnitForm(), name: "app", image: "x" })).toBe(false);
  });

  it("est vrai dès qu'un réglage technique est rempli", () => {
    expect(hasAdvancedUnitValues({ ...emptyUnitForm(), ports: "80" })).toBe(true);
    expect(hasAdvancedUnitValues({ ...emptyUnitForm(), dataPath: "/data" })).toBe(true);
    expect(hasAdvancedUnitValues({ ...emptyUnitForm(), updatePolicy: "hold" })).toBe(true);
  });
});

describe("acceptanceOf", () => {
  it("crée un service neuf avec la proposition telle quelle", () => {
    const body = acceptanceOf(proposal());
    expect(body).toMatchObject({ machine_id: "m2", name: "redis", origin: "compose", compose_project: "nextcloud", category: "" });
    // Une unité proposée se suit : le serveur ne dit pas sa politique.
    expect(body.units.map((unit) => [unit.name, unit.update_policy])).toEqual([["redis", "watch"]]);
  });

  it("envoie ce que l'assistant enverrait sans y toucher", () => {
    const proposed = proposal();
    expect(acceptanceOf(proposed)).toEqual(serviceRequestOf(serviceFormOfDefinition(proposed.definition)));
  });
});
