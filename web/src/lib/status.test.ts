import { describe, expect, it } from "vitest";

import type { Incident, StatusComponent } from "../api/status";
import {
  componentFormOf,
  componentRequestOf,
  emptyComponentForm,
  emptyIncidentForm,
  formatPercent,
  fromLocalInput,
  incidentEditFormOf,
  incidentEditRequestOf,
  incidentRequestOf,
  incidentsSubtitle,
  incidentUpdateFormOf,
  incidentUpdateRequestOf,
  openingStatuses,
  saysNothing,
  statusesFor,
  componentChoiceLabel,
  componentLookupPath,
  componentPath,
  editIncidentPath,
  newComponentPath,
  publicPagePath,
  publicStatusPaths,
  toLocalInput,
  updateStatusesFor,
  worstState,
} from "./status";

const t = (key: string, ...args: (string | number)[]) => [key, ...args].join(" ");

describe("formatPercent", () => {
  it("rend rien sans essai, jamais un zéro", () => {
    expect(formatPercent(0, 0, "fr")).toBeNull();
  });
  it("garde la virgule française et deux décimales au plus", () => {
    expect(formatPercent(1000, 999, "fr")).toBe("99,9");
    expect(formatPercent(3, 2, "en")).toBe("66.67");
    expect(formatPercent(100, 100, "en")).toBe("100");
  });
  it("ne dit jamais 100 pour une disponibilité entamée", () => {
    expect(formatPercent(100000, 99999, "en")).toBe("99.99");
  });
});

describe("statusesFor", () => {
  it("planifie une maintenance et analyse un incident", () => {
    expect(statusesFor("maintenance")).toEqual(["scheduled", "in_progress", "resolved"]);
    expect(statusesFor("down")[0]).toBe("investigating");
  });
});

describe("incidentsSubtitle", () => {
  it("dit rien, tout fermé, ou le compte des ouverts", () => {
    expect(incidentsSubtitle(t, 0, 0)).toBe("incident.subtitle_none");
    expect(incidentsSubtitle(t, 3, 0)).toBe("incident.subtitle_ok 3");
    expect(incidentsSubtitle(t, 3, 1)).toBe("incident.subtitle_open 3 1");
  });
});

describe("datetime-local", () => {
  it("fait l'aller-retour entre l'heure locale et l'instant ISO", () => {
    const iso = new Date(2026, 8, 26, 8, 0).toISOString();
    expect(toLocalInput(iso)).toBe("2026-09-26T08:00");
    expect(fromLocalInput("2026-09-26T08:00")).toBe(iso);
    expect(toLocalInput(null)).toBe("");
    expect(fromLocalInput("")).toBeNull();
    expect(fromLocalInput("n'importe quoi")).toBeNull();
  });
});

const windowStart = new Date(2026, 8, 26, 8, 0);
const windowEnd = new Date(2026, 8, 26, 10, 0);

function incident(overrides: Partial<Incident>): Incident {
  return {
    id: "inc-1",
    title: "Panne du site",
    impact: "down",
    status: "identified",
    starts_at: null,
    ends_at: null,
    targets: [{ kind: "machine", id: "m1", name: "Serveur" }],
    components: [{ id: "c1", name: "Site" }],
    updates: [],
    created_at: "2026-09-26T07:00:00Z",
    updated_at: "2026-09-26T07:00:00Z",
    resolved_at: null,
    ...overrides,
  };
}

describe("openingStatuses", () => {
  it("n'ouvre jamais un incident déjà résolu", () => {
    expect(openingStatuses).toEqual(["investigating", "identified", "monitoring"]);
  });
});

describe("emptyIncidentForm", () => {
  it("part d'un incident en panne, sans cible, et ne partage pas sa liste", () => {
    const form = emptyIncidentForm();
    expect(form).toMatchObject({ kind: "incident", impact: "down", status: "investigating", targets: [] });
    expect(emptyIncidentForm().targets).not.toBe(form.targets);
  });
});

describe("incidentRequestOf", () => {
  it("rend l'incident tel que tapé, sans fenêtre", () => {
    const values = { ...emptyIncidentForm(), title: "Panne", impact: "degraded" as const, status: "monitoring" as const, message: "On regarde", targets: [{ kind: "machine" as const, id: "m1" }], startsAt: "2026-09-26T08:00" };
    expect(incidentRequestOf(values)).toEqual({
      title: "Panne",
      impact: "degraded",
      status: "monitoring",
      message: "On regarde",
      targets: [{ kind: "machine", id: "m1" }],
      starts_at: null,
      ends_at: null,
    });
  });
  it("planifie une maintenance : impact et statut imposés, fenêtre en instants ISO", () => {
    const values = { ...emptyIncidentForm(), kind: "maintenance" as const, title: "Mise à jour", targets: [{ kind: "machine" as const, id: "m1" }], startsAt: "2026-09-26T08:00", endsAt: "2026-09-26T10:00" };
    expect(incidentRequestOf(values)).toMatchObject({
      impact: "maintenance",
      status: "scheduled",
      starts_at: windowStart.toISOString(),
      ends_at: windowEnd.toISOString(),
    });
  });
});

describe("updateStatusesFor", () => {
  it("ne propose jamais « planifié » pour une entrée du fil", () => {
    expect(updateStatusesFor("maintenance")).toEqual(["in_progress", "resolved"]);
    expect(updateStatusesFor("down")).toEqual(["investigating", "identified", "monitoring", "resolved"]);
  });
});

describe("incidentUpdateFormOf", () => {
  it("part du statut courant et d'un message vide", () => {
    expect(incidentUpdateFormOf(incident({ status: "identified" }))).toEqual({ status: "identified", message: "" });
  });
  it("prend le premier statut proposé quand le courant n'en fait pas partie", () => {
    expect(incidentUpdateFormOf(incident({ impact: "maintenance", status: "scheduled" })).status).toBe("in_progress");
  });
});

describe("saysNothing", () => {
  it("refuse une entrée sans message qui garde le statut", () => {
    const current = incident({ status: "identified" });
    expect(saysNothing({ status: "identified", message: "  " }, current)).toBe(true);
    expect(saysNothing({ status: "resolved", message: "" }, current)).toBe(false);
    expect(saysNothing({ status: "identified", message: "Corrigé" }, current)).toBe(false);
  });
});

describe("incidentUpdateRequestOf", () => {
  it("rend le statut et le message", () => {
    expect(incidentUpdateRequestOf({ status: "resolved", message: "Fini" })).toEqual({ status: "resolved", message: "Fini" });
  });
});

describe("formulaire de correction", () => {
  it("part de l'incident : titre, cibles, fenêtre en heure locale", () => {
    const maintenance = incident({ impact: "maintenance", starts_at: windowStart.toISOString(), ends_at: windowEnd.toISOString() });
    expect(incidentEditFormOf(maintenance)).toEqual({ title: "Panne du site", targets: [{ kind: "machine" as const, id: "m1" }], startsAt: "2026-09-26T08:00", endsAt: "2026-09-26T10:00" });
  });
  it("renvoie la fenêtre d'une maintenance, et des dates nulles à un incident", () => {
    const values = { title: "Titre", targets: [{ kind: "machine" as const, id: "m1" }], startsAt: "2026-09-26T08:00", endsAt: "2026-09-26T10:00" };
    expect(incidentEditRequestOf(values, "maintenance")).toEqual({
      title: "Titre",
      targets: [{ kind: "machine", id: "m1" }],
      starts_at: windowStart.toISOString(),
      ends_at: windowEnd.toISOString(),
    });
    expect(incidentEditRequestOf(values, "down")).toMatchObject({ starts_at: null, ends_at: null });
  });
  it("relue puis renvoyée, une maintenance garde sa fenêtre", () => {
    const maintenance = incident({ impact: "maintenance", starts_at: windowStart.toISOString(), ends_at: windowEnd.toISOString() });
    const request = incidentEditRequestOf(incidentEditFormOf(maintenance), "maintenance");
    expect(request.starts_at).toBe(maintenance.starts_at);
    expect(request.ends_at).toBe(maintenance.ends_at);
  });
});

describe("componentFormOf et componentRequestOf", () => {
  const component: StatusComponent = {
    id: "cmp_1",
    project_id: "prj_1",
    name: "Cloud",
    position: 2,
    implicit: false,
    members: [
      { kind: "probe", id: "prb_1", name: "site", state: "operational", counts: true, present: true },
      { kind: "service", id: "svc_1", name: "nextcloud", state: "", counts: false, present: false },
    ],
    derived: "operational",
    effective: "operational",
    created_at: "2026-10-01T10:00:00Z",
  };

  it("renvoient au serveur le composant lu, objets réduits à leur genre et leur identifiant", () => {
    expect(componentRequestOf(componentFormOf(component))).toEqual({
      project_id: "prj_1",
      name: "Cloud",
      position: 2,
      members: [
        { kind: "probe", id: "prb_1" },
        { kind: "service", id: "svc_1" },
      ],
    });
  });

  it("partent d'un composant sans objet, en tête de page", () => {
    expect(componentRequestOf(emptyComponentForm("prj_1"))).toEqual({ project_id: "prj_1", name: "", position: 0, members: [] });
  });
});

describe("worstState", () => {
  it("garde le pire, comme le serveur : panne, maintenance, dégradé, opérationnel", () => {
    expect(worstState(["operational", "degraded"])).toBe("degraded");
    expect(worstState(["degraded", "maintenance"])).toBe("maintenance");
    expect(worstState(["maintenance", "down", "operational"])).toBe("down");
  });
  it("ne dit rien quand rien ne compte", () => {
    expect(worstState([])).toBe("");
    expect(worstState([""])).toBe("");
    expect(worstState(["", "operational"])).toBe("operational");
  });
});

describe("les adresses de Disponibilité", () => {
  it("échappent l'identifiant et ouvrent les fenêtres sur la page de leur projet", () => {
    expect(publicPagePath("prj_a b")).toBe("/disponibilite/pages/prj_a%20b");
    expect(newComponentPath("prj_1")).toBe("/disponibilite/pages/prj_1/composants/nouveau");
    expect(componentPath("prj_1", "cmp_1")).toBe("/disponibilite/pages/prj_1/composants/cmp_1");
  });

  it("retrouvent un composant par son seul identifiant, et l'incident à corriger", () => {
    expect(componentLookupPath("cmp 1")).toBe("/disponibilite/composants/cmp%201");
    expect(editIncidentPath("inc_1")).toBe("/disponibilite/incidents/inc_1/modifier");
  });
});

describe("componentChoiceLabel", () => {
  const projects = [
    { id: "prj_1", name: "client-a", title: "Client A" },
    { id: "prj_2", name: "client-b", title: "" },
  ];
  it("ne nomme le projet qu'à partir de deux, deux projets pouvant avoir chacun leur « Site web »", () => {
    expect(componentChoiceLabel({ name: "Site web", project_id: "prj_1" }, projects.slice(0, 1))).toBe("Site web");
    expect(componentChoiceLabel({ name: "Site web", project_id: "prj_1" }, projects)).toBe("Client A · Site web");
    expect(componentChoiceLabel({ name: "Site web", project_id: "prj_2" }, projects)).toBe("client-b · Site web");
    expect(componentChoiceLabel({ name: "Site web", project_id: "" }, projects)).toBe("Site web");
  });
});

describe("publicStatusPaths", () => {
  it("tire les chemins de l'API publique du slug de l'adresse", () => {
    expect(publicStatusPaths("/statut/client-a")).toEqual({
      status: "/statut/client-a/api/status",
      catalog: "/statut/client-a/api/i18n",
      events: "/statut/client-a/api/events",
    });
    expect(publicStatusPaths("/statut/client-a/")?.status).toBe("/statut/client-a/api/status");
  });
  it("refuse une adresse qui n'est pas celle d'une page", () => {
    for (const pathname of ["/statut", "/statut/", "/statut/a/b", "/statut/Client", "/statut/-a", "/", "/statutx/a"]) {
      expect(publicStatusPaths(pathname)).toBeNull();
    }
  });
});
