import { describe, expect, it } from "vitest";

import { formatPercent, fromLocalInput, incidentsSubtitle, statusesFor, toLocalInput, uptimeOf } from "./status";

const t = (key: string, ...args: (string | number)[]) => [key, ...args].join(" ");

describe("uptimeOf", () => {
  it("additionne les jours d'un composant", () => {
    const days = [
      { day: "2026-09-19", total: 10, success: 10, degraded: 0 },
      { day: "2026-09-20", total: 10, success: 7, degraded: 1 },
    ];
    expect(uptimeOf(days)).toEqual({ total: 20, success: 17 });
    expect(uptimeOf([])).toEqual({ total: 0, success: 0 });
  });
});

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
