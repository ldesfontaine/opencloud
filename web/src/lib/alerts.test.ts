import { describe, expect, it } from "vitest";

import type { Alert, Channel } from "../api/alerts";
import {
  alertFilterOf,
  alertsSubtitle,
  channelFormOf,
  channelRequestOf,
  deliveryReason,
  describeAlert,
  deliveryFailure,
  emptyChannelForm,
  canAnnounce,
  filterAlerts,
  filterChoicesOf,
  inboxOf,
  isFiltering,
  emptyMuteForm,
  muteRequestOf,
  mutePathOf,
  muteStateOf,
  muteTargets,
  mostUrgentAlert,
  objectPath,
  renamedAlertLocation,
  stateOf,
  withAlertFilter,
} from "./alerts";

const t = (key: string, ...args: (string | number)[]) => [key, ...args].join(" ");
const now = Date.parse("2026-09-22T10:00:00Z");
const disks = { cpu: { attention: 95, danger: 99 }, memory: { attention: 90, danger: 95 }, disk: { attention: 85, danger: 95 } };

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
  it("dit la moyenne du processeur sur sa fenêtre, avec le seuil noté à l'ouverture", () => {
    const processor = alert({ kind: "cpu_high", object: { kind: "machine", id: "local", name: "vps-lyon-2" }, details: { percent: 97, window_minutes: 30, threshold: 98 } });
    const text = describeAlert(t, processor, now, disks);
    expect(text.fact).toBe("alert.fact_cpu_high 97");
    expect(text.cause).toBe("alert.cause_cpu_high vps-lyon-2 30 98");
    expect(text.action).toBe("alert.action_cpu_high");
  });
  it("une alerte de mémoire ouverte avant que sa fenêtre ne soit notée cite celle du produit et le seuil de la session", () => {
    const memory = alert({ kind: "memory_high", object: { kind: "machine", id: "local", name: "vps-lyon-2" }, details: { percent: 93 } });
    expect(describeAlert(t, memory, now, disks).cause).toBe("alert.cause_memory_high vps-lyon-2 15 90");
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
  it("distingue non vue, vue, résolue", () => {
    expect(stateOf(alert({}))).toBe("unseen");
    expect(stateOf(alert({ acknowledged_at: "2026-09-22T09:30:00Z" }))).toBe("seen");
    expect(stateOf(alert({ status: "resolved", resolved_at: "2026-09-22T09:30:00Z" }))).toBe("resolved");
  });
});

describe("objectPath", () => {
  it("mène à la fiche de l'objet, un volume à sa machine", () => {
    expect(objectPath({ kind: "probe", id: "p1", name: "site" }, "")).toBe("/disponibilite/p1");
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
      announce: false,
      public_title: "",
    });
  });
  it("n'annonce qu'un objet du répertoire, avec son titre public", () => {
    const values = { ...emptyMuteForm, targetKind: "service" as const, targetID: "s1", announce: true, publicTitle: "Mise à jour du site" };
    expect(muteRequestOf(values)).toMatchObject({ announce: true, public_title: "Mise à jour du site" });
    expect(muteRequestOf({ ...values, targetKind: "container" })).toMatchObject({ announce: false, public_title: "" });
    expect(canAnnounce({ targetKind: "", targetID: "" })).toBe(false);
    expect(canAnnounce({ targetKind: "machine", targetID: "" })).toBe(false);
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
    expect(mutePathOf(alert({}))).toBe("/parametres/silences/nouveau?kind=disk_full&target_kind=machine&target_id=local&target_name=vps-lyon-2");
  });
  it("tait un conteneur par son service déclaré, ou lui seul s'il n'est pas déclaré", () => {
    const container = { kind: "container" as const, id: "local:abc", name: "shop-app-1" };
    expect(mutePathOf(alert({ kind: "service_down", object: container, service_id: "svc_1" }))).toBe(
      "/parametres/silences/nouveau?kind=service_down&target_kind=service&target_id=svc_1&target_name=",
    );
    expect(mutePathOf(alert({ kind: "service_down", object: container }))).toBe(
      "/parametres/silences/nouveau?kind=service_down&target_kind=container&target_id=local%3Aabc&target_name=shop-app-1",
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

describe("mostUrgentAlert", () => {
  const acknowledged = "2026-09-22T09:30:00Z";

  it("ne rend rien sans alerte ouverte", () => {
    expect(mostUrgentAlert([])).toBeNull();
    expect(mostUrgentAlert([alert({ status: "resolved", resolved_at: "2026-09-22T09:40:00Z" })])).toBeNull();
  });

  it("une alerte non acquittée passe avant une acquittée, même plus grave", () => {
    const grave = alert({ id: 1, severity: "danger", acknowledged_at: acknowledged });
    const open = alert({ id: 2, severity: "attention" });
    expect(mostUrgentAlert([grave, open])?.id).toBe(2);
  });

  it("à acquittement égal, la plus grave passe avant, même plus ancienne", () => {
    const old = alert({ id: 1, severity: "danger", opened_at: "2026-09-20T09:00:00Z" });
    const recent = alert({ id: 2, severity: "attention", opened_at: "2026-09-22T09:00:00Z" });
    expect(mostUrgentAlert([recent, old])?.id).toBe(1);
  });

  it("à égalité de tout, la plus récente", () => {
    const old = alert({ id: 1, opened_at: "2026-09-20T09:00:00Z" });
    const recent = alert({ id: 2, opened_at: "2026-09-22T09:00:00Z" });
    expect(mostUrgentAlert([old, recent])?.id).toBe(2);
    expect(mostUrgentAlert([recent, old])?.id).toBe(2);
  });

  it("une résolue n'est jamais la plus urgente", () => {
    const resolved = alert({ id: 1, severity: "danger", status: "resolved", resolved_at: "2026-09-22T09:40:00Z" });
    expect(mostUrgentAlert([resolved, alert({ id: 2, acknowledged_at: acknowledged })])?.id).toBe(2);
  });
});

describe("inboxOf", () => {
  const seenAt = "2026-09-22T09:30:00Z";
  it("met les non vues en tête, les vues ensuite, chacune dans l'ordre du serveur", () => {
    const list = [alert({ id: 1, acknowledged_at: seenAt }), alert({ id: 2 }), alert({ id: 3, acknowledged_at: seenAt }), alert({ id: 4 })];
    const inbox = inboxOf(list);
    expect(inbox.unseen.map((entry) => entry.id)).toEqual([2, 4]);
    expect(inbox.seen.map((entry) => entry.id)).toEqual([1, 3]);
  });
  it("ne garde pas une résolue : elle est de l'historique", () => {
    const inbox = inboxOf([alert({ id: 1, status: "resolved", resolved_at: seenAt })]);
    expect(inbox).toEqual({ unseen: [], seen: [] });
  });
});

describe("filtres de la boîte", () => {
  const emptyAlertFilter = alertFilterOf(new URLSearchParams());
  const list = [
    alert({ id: 1, kind: "disk_full", severity: "danger", machine_id: "m1", machine_name: "vps-lyon-2" }),
    alert({ id: 2, kind: "service_down", severity: "attention", machine_id: "m2", machine_name: "nas", service_id: "svc_1" }),
    alert({ id: 3, kind: "job_late", severity: "attention", machine_id: "", machine_name: "" }),
  ];
  const ids = (filtered: Alert[]) => filtered.map((entry) => entry.id);

  it("ne filtre rien sans choix", () => {
    expect(isFiltering(emptyAlertFilter)).toBe(false);
    expect(ids(filterAlerts(list, emptyAlertFilter))).toEqual([1, 2, 3]);
  });
  it("filtre par gravité, genre, machine et service, et les croise", () => {
    expect(ids(filterAlerts(list, { ...emptyAlertFilter, severity: "attention" }))).toEqual([2, 3]);
    expect(ids(filterAlerts(list, { ...emptyAlertFilter, kind: "disk_full" }))).toEqual([1]);
    expect(ids(filterAlerts(list, { ...emptyAlertFilter, machineID: "m2" }))).toEqual([2]);
    expect(ids(filterAlerts(list, { ...emptyAlertFilter, serviceID: "svc_1" }))).toEqual([2]);
    expect(ids(filterAlerts(list, { ...emptyAlertFilter, severity: "attention", kind: "disk_full" }))).toEqual([]);
    expect(isFiltering({ ...emptyAlertFilter, kind: "disk_full" })).toBe(true);
  });
  it("lit le filtre dans l'adresse et ignore une valeur inconnue", () => {
    const params = new URLSearchParams("gravite=danger&genre=disk_full&machine=m1&service=svc_1");
    expect(alertFilterOf(params)).toEqual({ severity: "danger", kind: "disk_full", machineID: "m1", serviceID: "svc_1" });
    expect(alertFilterOf(new URLSearchParams("gravite=critique&genre=nimporte"))).toEqual(emptyAlertFilter);
  });
  it("pose un choix dans l'adresse, et le retire quand il est vide, sans toucher aux autres", () => {
    const params = new URLSearchParams("genre=disk_full");
    expect(withAlertFilter(params, "severity", "danger").toString()).toBe("genre=disk_full&gravite=danger");
    expect(withAlertFilter(params, "kind", "").toString()).toBe("");
    expect(params.toString()).toBe("genre=disk_full");
  });
  it("ne propose que ce que les alertes portent", () => {
    expect(filterChoicesOf(list, emptyAlertFilter)).toEqual({
      kinds: ["service_down", "job_late", "disk_full"],
      machines: [
        { id: "m2", name: "nas" },
        { id: "m1", name: "vps-lyon-2" },
      ],
      serviceIDs: ["svc_1"],
    });
  });
  it("garde dans les menus un choix que plus aucune alerte ne porte", () => {
    const choices = filterChoicesOf(list, { ...emptyAlertFilter, kind: "probe_down", machineID: "m9", serviceID: "svc_9" });
    expect(choices.kinds).toContain("probe_down");
    expect(choices.machines).toContainEqual({ id: "m9", name: "m9" });
    expect(choices.serviceIDs).toContain("svc_9");
  });
});

describe("renamedAlertLocation", () => {
  it("mène les onglets Canaux et Silences à leur section de Paramètres", () => {
    expect(renamedAlertLocation("/alertes/canaux")).toEqual({ pathname: "/parametres", hash: "#notifications" });
    expect(renamedAlertLocation("/alertes/silences")).toEqual({ pathname: "/parametres", hash: "#notifications" });
  });
  it("mène une fenêtre d'avant à la même fenêtre sous Paramètres", () => {
    expect(renamedAlertLocation("/alertes/canaux/nouveau")).toEqual({ pathname: "/parametres/canaux/nouveau", hash: "" });
    expect(renamedAlertLocation("/alertes/canaux/12")).toEqual({ pathname: "/parametres/canaux/12", hash: "" });
    expect(renamedAlertLocation("/alertes/silences/nouveau")).toEqual({ pathname: "/parametres/silences/nouveau", hash: "" });
  });
  it("mène les autres onglets à la boîte", () => {
    expect(renamedAlertLocation("/alertes/acquittees")).toEqual({ pathname: "/alertes", hash: "" });
    expect(renamedAlertLocation("/alertes/resolues")).toEqual({ pathname: "/alertes", hash: "" });
  });
});

describe("la supervision des sauvegardes", () => {
  const backupAlert = (kind: Alert["kind"], details: Alert["details"]): Alert => ({
    id: 1,
    kind,
    severity: "attention",
    status: "open",
    muted: false,
    object: { kind: "backup", id: "restic:b1", name: "paperless" },
    machine_id: "m1",
    machine_name: "docker1",
    service_id: "",
    details,
    opened_at: "2026-11-02T10:00:00Z",
    updated_at: "2026-11-02T10:00:00Z",
    resolved_at: null,
    acknowledged_at: null,
  });

  it("un objet de sauvegarde mène à la page Sauvegardes et se tait par sa machine", () => {
    const alert = backupAlert("backup_stale", {});
    expect(objectPath(alert.object, "m1")).toBe("/sauvegardes");
    expect(mutePathOf(alert)).toContain("target_kind=machine");
    expect(mutePathOf(alert)).toContain("target_id=m1");
  });

  it("chaque constat a ses trois lignes dans le catalogue", () => {
    const text = (kind: Alert["kind"], details: Alert["details"]) => describeAlert(t, backupAlert(kind, details), Date.parse("2026-11-02T12:00:00Z"), disks);
    expect(text("backup_stale", { since: "2026-11-01T00:00:00Z", reason: "failed" }).cause).toContain("alert.backup_stale_failed");
    expect(text("backup_stale", { since: "2026-11-01T00:00:00Z" }).cause).toContain("alert.backup_stale_none");
    expect(text("restore_test_stale", { since: "2026-10-01T12:00:00Z", reason: "never_verified" }).cause).toMatch(/^alert.cause_restore_test_stale /);
    expect(text("restore_test_stale", { since: "2026-10-01T12:00:00Z", reason: "verify_right_missing" }).action).toBe("alert.action_restore_test_stale_right");
    expect(text("restore_test_stale", { since: "2026-10-01T12:00:00Z" }).action).toBe("alert.action_restore_test_stale");
    expect(text("source_unprotected", {}).fact).toBe("alert.fact_source_unprotected");
    expect(text("target_full", { bytes: 300 * 2 ** 20, count: 3 }).cause).toBe("alert.cause_target_full paperless 300 3");
    expect(text("target_unreachable", { reason: "offline", count: 2 }).cause).toContain("alert.target_unreachable_offline");
    expect(text("target_unreachable", { reason: "autre", count: 2 }).cause).toContain("alert.target_unreachable_error");
  });
});
