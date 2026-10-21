import { describe, expect, it } from "vitest";

import type { Alert } from "../api/alerts";
import { acknowledgedMessage, coverage, groupByMachine, keepSelectable, selectableIDs, toggleAll } from "./alertSelection";

const t = (key: string, ...args: (string | number)[]) => [key, ...args].join(" ");

function alert(id: number, overrides: Partial<Alert> = {}): Alert {
  return {
    id,
    kind: "disk_full",
    severity: "attention",
    status: "open",
    muted: false,
    object: { kind: "volume", id: `local:/v${id}`, name: `/v${id}` },
    machine_id: "local",
    machine_name: "opencloud",
    service_id: "",
    details: {},
    opened_at: "2026-10-10T09:00:00Z",
    updated_at: "2026-10-10T09:00:00Z",
    resolved_at: null,
    acknowledged_at: null,
    ...overrides,
  };
}

describe("groupByMachine", () => {
  it("regroupe par machine dans l'ordre de la première alerte, sans réordonner les alertes", () => {
    const groups = groupByMachine([
      alert(1, { machine_id: "b", machine_name: "vps-b", severity: "danger" }),
      alert(2),
      alert(3, { machine_id: "b", machine_name: "vps-b" }),
      alert(4, { machine_id: "", machine_name: "" }),
    ]);
    expect(groups.map((group) => [group.machineName, group.alerts.map((found) => found.id)])).toEqual([
      ["vps-b", [1, 3]],
      ["opencloud", [2]],
      ["", [4]],
    ]);
  });
});

describe("la sélection", () => {
  it("ne propose que les ouvertes non acquittées", () => {
    const alerts = [alert(1), alert(2, { acknowledged_at: "2026-10-10T09:30:00Z" }), alert(3, { status: "resolved", resolved_at: "2026-10-10T09:40:00Z" })];
    expect(selectableIDs(alerts)).toEqual([1]);
  });

  it("perd d'elle-même une alerte acquittée ou résolue entre-temps", () => {
    const kept = keepSelectable(new Set([1, 2, 9]), [alert(1), alert(2, { acknowledged_at: "2026-10-10T09:30:00Z" })]);
    expect([...kept]).toEqual([1]);
    const unchanged = new Set([1]);
    expect(keepSelectable(unchanged, [alert(1)])).toBe(unchanged);
  });

  it("complète un ensemble à moitié coché, puis le décoche une fois plein", () => {
    const half = new Set([1]);
    const full = toggleAll(half, [1, 2, 3]);
    expect([...full].sort()).toEqual([1, 2, 3]);
    expect(coverage(half, [1, 2, 3])).toBe("some");
    expect(coverage(full, [1, 2, 3])).toBe("all");
    expect(coverage(toggleAll(full, [1, 2, 3]), [1, 2, 3])).toBe("none");
  });

  it("ne touche que l'ensemble visé", () => {
    const next = toggleAll(new Set([7]), [1, 2]);
    expect([...next].sort()).toEqual([1, 2, 7]);
  });
});

describe("acknowledgedMessage", () => {
  it("dit le nombre, au singulier comme au pluriel, et le cas où rien n'a changé", () => {
    expect(acknowledgedMessage(t, 4)).toBe("alerts.acknowledged_many 4");
    expect(acknowledgedMessage(t, 1)).toBe("alerts.acknowledged_one");
    expect(acknowledgedMessage(t, 0)).toBe("alerts.acknowledged_none");
  });
});
