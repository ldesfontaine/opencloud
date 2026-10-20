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
  statusPageFormOf,
  toggleComponent,
  toLocalInput,
  updateStatusesFor,
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
  it("part d'un incident en panne, sans composant, et ne partage pas sa liste", () => {
    const form = emptyIncidentForm();
    expect(form).toMatchObject({ kind: "incident", impact: "down", status: "investigating", componentIDs: [] });
    expect(emptyIncidentForm().componentIDs).not.toBe(form.componentIDs);
  });
});

describe("incidentRequestOf", () => {
  it("rend l'incident tel que tapé, sans fenêtre", () => {
    const values = { ...emptyIncidentForm(), title: "Panne", impact: "degraded" as const, status: "monitoring" as const, message: "On regarde", componentIDs: ["c1"], startsAt: "2026-09-26T08:00" };
    expect(incidentRequestOf(values)).toEqual({
      title: "Panne",
      impact: "degraded",
      status: "monitoring",
      message: "On regarde",
      component_ids: ["c1"],
      starts_at: null,
      ends_at: null,
    });
  });
  it("planifie une maintenance : impact et statut imposés, fenêtre en instants ISO", () => {
    const values = { ...emptyIncidentForm(), kind: "maintenance" as const, title: "Mise à jour", componentIDs: ["c1"], startsAt: "2026-09-26T08:00", endsAt: "2026-09-26T10:00" };
    expect(incidentRequestOf(values)).toMatchObject({
      impact: "maintenance",
      status: "scheduled",
      starts_at: windowStart.toISOString(),
      ends_at: windowEnd.toISOString(),
    });
  });
});

describe("toggleComponent", () => {
  it("ajoute puis retire un composant sans modifier la liste reçue", () => {
    const selected = ["c1"];
    expect(toggleComponent(selected, "c2")).toEqual(["c1", "c2"]);
    expect(toggleComponent(selected, "c1")).toEqual([]);
    expect(selected).toEqual(["c1"]);
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
  it("part de l'incident : titre, composants, fenêtre en heure locale", () => {
    const maintenance = incident({ impact: "maintenance", starts_at: windowStart.toISOString(), ends_at: windowEnd.toISOString() });
    expect(incidentEditFormOf(maintenance)).toEqual({ title: "Panne du site", componentIDs: ["c1"], startsAt: "2026-09-26T08:00", endsAt: "2026-09-26T10:00" });
  });
  it("renvoie la fenêtre d'une maintenance, et des dates nulles à un incident", () => {
    const values = { title: "Titre", componentIDs: ["c1"], startsAt: "2026-09-26T08:00", endsAt: "2026-09-26T10:00" };
    expect(incidentEditRequestOf(values, "maintenance")).toEqual({
      title: "Titre",
      component_ids: ["c1"],
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
    name: "Cloud",
    position: 2,
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
      name: "Cloud",
      position: 2,
      members: [
        { kind: "probe", id: "prb_1" },
        { kind: "service", id: "svc_1" },
      ],
    });
  });

  it("partent d'un composant sans objet, en tête de page", () => {
    expect(componentRequestOf(emptyComponentForm())).toEqual({ name: "", position: 0, members: [] });
  });
});

describe("statusPageFormOf", () => {
  it("garde ce qui se règle, pas l'adresse publique", () => {
    const settings = { title: "État", announcement: "Travaux jeudi", language: "fr", public_url: "https://statut.exemple.fr" };
    expect(statusPageFormOf(settings)).toEqual({ title: "État", announcement: "Travaux jeudi", language: "fr" });
  });
});
