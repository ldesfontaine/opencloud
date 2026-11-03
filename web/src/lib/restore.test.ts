import { describe, expect, it } from "vitest";

import { restoreDestination, restoreSourcePath, restoreStamp } from "./restore";

describe("restoreStamp", () => {
  it("écrit AAAAMMJJ-HHMMSS, zéros compris", () => {
    expect(restoreStamp(new Date(2026, 8, 2, 4, 5, 6))).toBe("20260902-040506");
  });
});

describe("restoreDestination", () => {
  it("pose la copie à côté de l'original", () => {
    expect(restoreDestination("/srv/appli/config", "20260912-140000")).toBe("/srv/appli/config.restaure-20260912-140000");
  });
});

describe("restoreSourcePath", () => {
  it("un seul nom coché, sinon le dossier ouvert, jamais la racine ni plusieurs", () => {
    expect(restoreSourcePath("/srv/appli", ["config"])).toBe("/srv/appli/config");
    expect(restoreSourcePath("/srv/appli", [])).toBe("/srv/appli");
    expect(restoreSourcePath("/", [])).toBeNull();
    expect(restoreSourcePath("/srv", ["a", "b"])).toBeNull();
  });
});
