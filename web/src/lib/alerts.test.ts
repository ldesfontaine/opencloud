import { describe, expect, it } from "vitest";

import type { Alert } from "../api/types";
import { alertsSubtitle, deliveryReason, describeAlert, objectPath, silenceTargets, stateOf } from "./alerts";

const t = (key: string, ...args: (string | number)[]) => [key, ...args].join(" ");
const now = Date.parse("2026-09-22T10:00:00Z");
const disks = { attention: 85, danger: 95 };

function alert(overrides: Partial<Alert>): Alert {
  return {
    id: 1,
    kind: "disk_full",
    severity: "attention",
    status: "open",
    silenced: false,
    object: { kind: "volume", id: "local:/data", name: "/data" },
    machine_id: "local",
    machine_name: "vps-lyon-2",
    details: { mount_point: "/data", percent: 86 },
    opened_at: "2026-09-22T09:00:00Z",
    updated_at: "2026-09-22T09:00:00Z",
    resolved_at: null,
    acknowledged_at: null,
    ...overrides,
  };
}

describe("describeAlert", () => {
  it("dit le fait, la cause avec le seuil, puis le geste", () => {
    const text = describeAlert(t, alert({}), now, disks);
    expect(text.fact).toBe("alert.fact_disk_full 86");
    expect(text.cause).toBe("alert.cause_disk_full /data vps-lyon-2 85");
    expect(text.action).toBe("alert.action_disk_full");
  });
  it("compte les jours restants d'un certificat, arrondis vers le haut", () => {
    const text = describeAlert(t, alert({ kind: "cert_expiring", details: { target: "blog.exemple.fr", not_after: "2026-10-04T09:00:00Z" } }), now, disks);
    expect(text.cause).toBe("alert.cause_cert_expiring blog.exemple.fr 12");
  });
  it("change de fait quand un redémarrage devient une boucle", () => {
    const loop = describeAlert(t, alert({ kind: "service_restart", severity: "danger", details: { count: 4, exit_code: 2 } }), now, disks);
    expect(loop.fact).toBe("alert.fact_service_restart_loop");
    expect(loop.cause).toBe("alert.cause_service_restart_loop /data vps-lyon-2 4 2");
    const once = describeAlert(t, alert({ kind: "service_restart", details: { count: 1, exit_code: 2 } }), now, disks);
    expect(once.fact).toBe("alert.fact_service_restart");
  });
  it("met un tiret à la place d'une machine absente", () => {
    const text = describeAlert(t, alert({ kind: "service_down", machine_name: "", details: { exit_code: 1 } }), now, disks);
    expect(text.cause).toBe("alert.cause_service_down /data alert.no_machine 1");
  });
});

describe("stateOf", () => {
  it("distingue ouverte, acquittée, résolue", () => {
    expect(stateOf(alert({}))).toBe("open");
    expect(stateOf(alert({ acknowledged_at: "2026-09-22T09:30:00Z" }))).toBe("acknowledged");
    expect(stateOf(alert({ status: "resolved", resolved_at: "2026-09-22T09:30:00Z" }))).toBe("resolved");
  });
});

describe("objectPath", () => {
  it("mène à la fiche de l'objet, un volume à sa machine", () => {
    expect(objectPath({ kind: "probe", id: "p1", name: "site" }, "")).toBe("/domaines/p1");
    expect(objectPath({ kind: "volume", id: "m1:/", name: "/" }, "m1")).toBe("/machines/m1");
    expect(objectPath({ kind: "volume", id: "m1:/", name: "/" }, "")).toBeNull();
  });
});

describe("alertsSubtitle", () => {
  it("dit rien, tout acquitté, ou ce qui reste", () => {
    expect(alertsSubtitle(t, 0, 0)).toBe("alerts.subtitle_none");
    expect(alertsSubtitle(t, 2, 0)).toBe("alerts.subtitle_acknowledged 2");
    expect(alertsSubtitle(t, 3, 1)).toBe("alerts.subtitle_open 3 1");
  });
});

describe("deliveryReason", () => {
  it("porte le code HTTP quand il y en a un", () => {
    expect(deliveryReason(t, { reason: "status", code: 503 })).toBe("alert.delivery_reason_status 503");
    expect(deliveryReason(t, { reason: "timeout", code: 0 })).toBe("alert.delivery_reason_timeout");
  });
});

describe("silenceTargets", () => {
  const silence = { id: 1, kind: "" as const, object: { kind: "" as const, id: "", name: "" }, reason: "", starts_at: "", ends_at: "", active: true, created_at: "" };
  it("nomme le type, l'objet, ou les deux", () => {
    expect(silenceTargets(t, { ...silence, kind: "job_late" })).toBe("silence.targets_kind alert.fact_job_late 0");
    expect(silenceTargets(t, { ...silence, object: { kind: "machine", id: "m1", name: "vps" } })).toBe("silence.targets_object vps");
    expect(silenceTargets(t, { ...silence, kind: "disk_full", object: { kind: "machine", id: "m1", name: "vps" } })).toBe("silence.targets_both alert.fact_disk_full 0 vps");
  });
});
