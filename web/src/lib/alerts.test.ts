import { describe, expect, it } from "vitest";

import type { Alert, Channel } from "../api/alerts";
import {
  alertsSubtitle,
  channelFormOf,
  channelRequestOf,
  deliveryReason,
  describeAlert,
  deliveryFailure,
  emptyChannelForm,
  emptyMuteForm,
  muteRequestOf,
  mutePathOf,
  muteStateOf,
  muteTargets,
  objectPath,
  stateOf,
} from "./alerts";

const t = (key: string, ...args: (string | number)[]) => [key, ...args].join(" ");
const now = Date.parse("2026-09-22T10:00:00Z");
const disks = { attention: 85, danger: 95 };

function alert(overrides: Partial<Alert>): Alert {
  return {
    id: 1,
    kind: "disk_full",
    severity: "attention",
    status: "open",
    muted: false,
    object: { kind: "volume", id: "local:/data", name: "/data" },
    machine_id: "local",
    machine_name: "vps-lyon-2",
    service_id: "",
    details: { mount_point: "/data", percent: 86 },
    opened_at: "2026-09-22T09:00:00Z",
    updated_at: "2026-09-22T09:00:00Z",
    resolved_at: null,
    acknowledged_at: null,
    ...overrides,
  };
}

describe("describeAlert", () => {
  it("cite le seuil noté par la dernière lecture plutôt que celui de la session", () => {
    const text = describeAlert(t, alert({ details: { mount_point: "/data", percent: 72, threshold: 70 } }), now, disks);
    expect(text.cause).toBe("alert.cause_disk_full /data vps-lyon-2 70");
  });
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
  it("dit qu'une tâche n'a jamais pingé depuis sa création", () => {
    const never = describeAlert(t, alert({ kind: "job_late", details: { reason: "never_pinged", since: "2026-09-22T09:57:00Z" } }), now, disks);
    expect(never.fact).toBe("alert.fact_job_late_never");
    expect(never.action).toBe("alert.action_job_late_never");
    expect(never.cause).toContain("alert.cause_job_late_never /data");
  });

  it("dit une boucle même quand le service est à l'arrêt", () => {
    const loop = describeAlert(t, alert({ kind: "service_down", severity: "danger", machine_name: "vps-lyon-2", details: { count: 3, exit_code: 137, reason: "oom" } }), now, disks);
    expect(loop.fact).toBe("alert.fact_service_restart_loop");
    expect(loop.cause).toBe("alert.cause_service_restart_loop /data vps-lyon-2 3 137");
  });
  it("met un tiret à la place d'une machine absente", () => {
    const text = describeAlert(t, alert({ kind: "service_down", machine_name: "", details: { exit_code: 1 } }), now, disks);
    expect(text.cause).toBe("alert.cause_service_down /data alert.no_machine 1");
  });
  it("dit un arrêt par manque de mémoire, pas un code de sortie", () => {
    const oom = describeAlert(t, alert({ kind: "service_down", machine_name: "vps-lyon-2", details: { exit_code: 137, reason: "oom" } }), now, disks);
    expect(oom.fact).toBe("alert.fact_service_down_oom");
    expect(oom.cause).toBe("alert.cause_service_down_oom /data vps-lyon-2");
    expect(oom.action).toBe("alert.action_service_down_oom");
    const killed = describeAlert(t, alert({ kind: "service_down", machine_name: "vps-lyon-2", details: { exit_code: 137, reason: "killed" } }), now, disks);
    expect(killed.fact).toBe("alert.fact_service_down_killed");
    expect(killed.cause).toBe("alert.cause_service_down_killed /data vps-lyon-2");
    expect(killed.action).toBe("alert.action_service_down_killed");
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
    expect(deliveryReason(t, { reason: "smtp_auth_failed", code: 535 })).toBe("alert.smtp_code alert.delivery_reason_smtp_auth_failed 535");
    expect(deliveryReason(t, { reason: "smtp_tls", code: 0 })).toBe("alert.delivery_reason_smtp_tls");
  });
});

describe("muteTargets", () => {
  const mute = { kind: "" as const, target: { kind: "" as const, id: "", name: "" } };
  it("nomme le type, l'objet, les deux, ou tout", () => {
    expect(muteTargets(t, { ...mute, kind: "job_late" })).toBe("silence.targets_kind alert.kind_job_late");
    expect(muteTargets(t, { ...mute, target: { kind: "machine", id: "m1", name: "vps" } })).toBe("silence.targets_object vps");
    expect(muteTargets(t, { kind: "disk_full", target: { kind: "machine", id: "m1", name: "vps" } })).toBe("silence.targets_both alert.kind_disk_full vps");
    expect(muteTargets(t, mute)).toBe("silence.targets_all");
  });
});

describe("muteStateOf", () => {
  it("dit en cours, planifié ou expiré", () => {
    expect(muteStateOf({ active: true, starts_at: "2026-09-22T09:00:00Z" }, now)).toBe("active");
    expect(muteStateOf({ active: false, starts_at: "2026-09-22T11:00:00Z" }, now)).toBe("planned");
    expect(muteStateOf({ active: false, starts_at: "2026-09-22T08:00:00Z" }, now)).toBe("expired");
  });
});

describe("muteRequestOf", () => {
  it("rend le silence tel que réglé, durée en minutes, début en instant", () => {
    const values = { ...emptyMuteForm, kind: "disk_full" as const, targetKind: "machine" as const, targetID: "m1", startsAt: "2026-09-26T08:00", minutes: 240, reason: "maintenance" };
    expect(muteRequestOf(values)).toEqual({
      kind: "disk_full",
      target_kind: "machine",
      target_id: "m1",
      reason: "maintenance",
      starts_at: new Date("2026-09-26T08:00").toISOString(),
      duration_minutes: 240,
    });
  });
  it("n'envoie pas de genre de cible quand aucun objet n'est choisi", () => {
    const request = muteRequestOf({ ...emptyMuteForm, kind: "job_late", targetKind: "probe" });
    expect(request).toMatchObject({ kind: "job_late", target_kind: "", target_id: "" });
  });
  it("part d'une heure, tout de suite, sans type ni objet", () => {
    expect(muteRequestOf(emptyMuteForm)).toMatchObject({ kind: "", target_kind: "", starts_at: null, duration_minutes: 60 });
  });
});

describe("mutePathOf", () => {
  it("tait un volume par le disque de sa machine", () => {
    expect(mutePathOf(alert({}))).toBe("/alertes/silences/nouveau?kind=disk_full&target_kind=machine&target_id=local&target_name=vps-lyon-2");
  });
  it("tait un conteneur par son service déclaré, ou lui seul s'il n'est pas déclaré", () => {
    const container = { kind: "container" as const, id: "local:abc", name: "shop-app-1" };
    expect(mutePathOf(alert({ kind: "service_down", object: container, service_id: "svc_1" }))).toBe(
      "/alertes/silences/nouveau?kind=service_down&target_kind=service&target_id=svc_1&target_name=",
    );
    expect(mutePathOf(alert({ kind: "service_down", object: container }))).toBe(
      "/alertes/silences/nouveau?kind=service_down&target_kind=container&target_id=local%3Aabc&target_name=shop-app-1",
    );
  });
});

describe("deliveryFailure", () => {
  it("ajoute l'heure de l'essai suivant quand il y en a un", () => {
    expect(deliveryFailure(t, { reason: "timeout", code: 0, next_attempt_at: null })).toBe("alert.delivery_reason_timeout");
    expect(deliveryFailure(t, { reason: "timeout", code: 0, next_attempt_at: "2026-09-22T10:02:00Z" })).toMatch(/^alert\.delivery_reason_timeout · alert\.delivery_retry /);
  });
});

describe("channelFormOf et channelRequestOf", () => {
  const matrix: Channel = {
    id: 3,
    name: "astreinte",
    url: "https://matrix.exemple.fr",
    format: "matrix",
    destination: "!salon:exemple.fr",
    has_secret: true,
    min_severity: "danger",
    notify_resolve: false,
    receives_audit: true,
    enabled: true,
    plain: false,
    created_at: "2026-10-01T10:00:00Z",
  };

  it("renvoient au serveur le canal lu, sans toucher au secret gardé", () => {
    expect(channelRequestOf(channelFormOf(matrix))).toEqual({
      name: "astreinte",
      url: "https://matrix.exemple.fr",
      format: "matrix",
      destination: "!salon:exemple.fr",
      secret: "",
      clear_secret: false,
      min_severity: "danger",
      notify_resolve: false,
      receives_audit: true,
      enabled: true,
    });
  });

  it("laissent vide la destination d'un webhook", () => {
    const webhook: Channel = { ...matrix, format: "json" };
    delete webhook.destination;
    expect(channelFormOf(webhook).destination).toBe("");
  });

  it("partent d'un canal JSON actif, qui prévient du retour à la normale", () => {
    expect(channelRequestOf(emptyChannelForm)).toMatchObject({ format: "json", min_severity: "attention", notify_resolve: true, enabled: true });
  });

  it("ne donnent pas le journal à un nouveau canal : il se coche", () => {
    expect(channelRequestOf(emptyChannelForm).receives_audit).toBe(false);
    expect(channelRequestOf({ ...emptyChannelForm, receivesAudit: true }).receives_audit).toBe(true);
  });
});
