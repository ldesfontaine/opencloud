import { describe, expect, it } from "vitest";

import type { ProbeDay } from "../api/types";
import { dayKeys, dayTone, uptimeOfDays, uptimePercent } from "./probes";

function day(date: string, total: number, success: number, degraded = 0): ProbeDay {
  return { day: date, total, success, degraded, duration_ms: 12 };
}

describe("uptimePercent", () => {
  it("rend rien plutôt qu'un zéro quand aucun essai n'est tombé", () => {
    expect(uptimePercent(0, 0)).toBeNull();
  });

  it("compte les succès sur les essais", () => {
    expect(uptimePercent(200, 199)).toBeCloseTo(99.5);
  });
});

describe("uptimeOfDays", () => {
  it("additionne les jours, parce que l'agrégat garde des comptes", () => {
    expect(uptimeOfDays([day("2026-09-17", 100, 100), day("2026-09-18", 100, 90)])).toEqual({ total: 200, success: 190 });
  });
});

describe("dayTone", () => {
  it("laisse gris un jour sans essai", () => {
    expect(dayTone(undefined)).toBe("none");
    expect(dayTone(day("2026-09-18", 0, 0))).toBe("none");
  });

  it("met en attention un jour entièrement dégradé", () => {
    expect(dayTone(day("2026-09-18", 10, 10, 10))).toBe("warn");
  });

  it("met en danger dès un échec", () => {
    expect(dayTone(day("2026-09-18", 10, 9))).toBe("danger");
  });

  it("laisse au vert un jour sans échec ni dégradation", () => {
    expect(dayTone(day("2026-09-18", 10, 10))).toBe("ok");
  });
});

describe("dayKeys", () => {
  it("rend les jours UTC du plus ancien à aujourd'hui", () => {
    const now = Date.parse("2026-09-19T23:30:00Z");
    expect(dayKeys(3, now)).toEqual(["2026-09-17", "2026-09-18", "2026-09-19"]);
  });

  it("traverse un changement de mois", () => {
    const now = Date.parse("2026-10-01T00:10:00Z");
    expect(dayKeys(2, now)).toEqual(["2026-09-30", "2026-10-01"]);
  });
});
