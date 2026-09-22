import type { Alert, AlertKind, AlertObject, AlertSeverity, Delivery, DiskThresholds, Silence } from "../api/types";
import type { Tone } from "../components/Pill";
import type { Translate } from "../i18n/context";
import { daysUntil } from "./certificates";
import { formatDuration, since } from "./time";

// Ce qu'une alerte dit : le fait, la cause, puis ce qu'on peut faire. Les
// mêmes clés que le canal, remplies ici avec les faits de l'API.
export interface AlertText {
  fact: string;
  cause: string;
  action: string;
}

export const severityTones: Record<AlertSeverity, Tone> = {
  attention: "warn",
  danger: "danger",
};

export function describeAlert(t: Translate, alert: Alert, now: number, disks: DiskThresholds): AlertText {
  const machine = alert.machine_name !== "" ? alert.machine_name : t("alert.no_machine");
  const object = alert.object.name;
  const details = alert.details;
  switch (alert.kind) {
    case "machine_lost":
      return lines(t, alert.kind, [], [object, formatDuration(t, details.since ? since(details.since, now) : 0)]);
    case "service_down":
      return lines(t, alert.kind, [], [object, machine, details.exit_code ?? 0]);
    case "service_unhealthy":
      return lines(t, alert.kind, [], [object, machine]);
    case "service_restart":
      if (alert.severity === "danger") {
        return {
          fact: t("alert.fact_service_restart_loop"),
          cause: t("alert.cause_service_restart_loop", object, machine, details.count ?? 0, details.exit_code ?? 0),
          action: t("alert.action_service_restart"),
        };
      }
      return lines(t, alert.kind, [], [object, machine, details.exit_code ?? 0]);
    case "job_late":
      return lines(t, alert.kind, [], [object]);
    case "job_failed":
      return lines(t, alert.kind, [], [object, details.exit_code ?? 0]);
    case "probe_down":
      return lines(t, alert.kind, [], [object, details.target ?? "", t(`probe.reason_${details.reason ?? "unreachable"}`)]);
    case "cert_expiring":
      return lines(t, alert.kind, [], [details.target ?? "", details.not_after ? daysUntil(details.not_after, now) : 0]);
    case "cert_expired":
      return lines(t, alert.kind, [], [details.target ?? "", details.not_after ? -daysUntil(details.not_after, now) : 0]);
    case "cert_untrusted":
      return lines(t, alert.kind, [], [details.target ?? "", t(`alert.reason_${details.reason ?? "chain"}`)]);
    case "disk_full":
      return lines(t, alert.kind, [details.percent ?? 0], [details.mount_point ?? object, machine, disks.attention]);
  }
}

function lines(t: Translate, kind: AlertKind, factArgs: (string | number)[], causeArgs: (string | number)[]): AlertText {
  return {
    fact: t(`alert.fact_${kind}`, ...factArgs),
    cause: t(`alert.cause_${kind}`, ...causeArgs),
    action: t(`alert.action_${kind}`),
  };
}

// L'état d'une alerte tel que la pastille le dit.
export type AlertState = "open" | "acknowledged" | "resolved";

export function stateOf(alert: Pick<Alert, "status" | "acknowledged_at">): AlertState {
  if (alert.status === "resolved") {
    return "resolved";
  }
  return alert.acknowledged_at !== null ? "acknowledged" : "open";
}

export const stateTones: Record<AlertState, Tone> = {
  open: "danger",
  acknowledged: "accent",
  resolved: "ok",
};

// Où mène un objet : sa fiche ; un volume, sa machine.
export function objectPath(object: AlertObject, machineID: string): string | null {
  switch (object.kind) {
    case "machine":
      return `/machines/${object.id}`;
    case "service":
      return `/services/${object.id}`;
    case "heartbeat":
      return `/taches/${object.id}`;
    case "probe":
      return `/domaines/${object.id}`;
    case "volume":
      return machineID !== "" ? `/machines/${machineID}` : null;
  }
}

// Le sous-titre de la page : rien, tout acquitté, ou ce qui reste à voir.
export function alertsSubtitle(t: Translate, open: number, unacknowledged: number): string {
  if (open === 0) {
    return t("alerts.subtitle_none");
  }
  if (unacknowledged === 0) {
    return t("alerts.subtitle_acknowledged", open);
  }
  return t("alerts.subtitle_open", open, unacknowledged);
}

// Pourquoi une livraison a échoué, par les mots du catalogue.
export function deliveryReason(t: Translate, delivery: Pick<Delivery, "reason" | "code">): string {
  if (delivery.reason === "status") {
    return t("alert.delivery_reason_status", delivery.code);
  }
  return t(`alert.delivery_reason_${delivery.reason}`);
}

export const deliveryTones: Record<Delivery["status"], Tone> = {
  pending: "neutral",
  delivered: "ok",
  failed: "danger",
};

// Ce qu'un silence vise, en une phrase : le type, l'objet, ou les deux.
export function silenceTargets(t: Translate, silence: Silence): string {
  const kind = silence.kind !== "" ? t(`alert.fact_${silence.kind}`, 0) : "";
  const object = silence.object.id !== "" ? silence.object.name : "";
  if (kind !== "" && object !== "") {
    return t("silence.targets_both", kind, object);
  }
  if (kind !== "") {
    return t("silence.targets_kind", kind);
  }
  return t("silence.targets_object", object);
}

// Les durées qu'un silence propose, en minutes.
export const silenceDurations: readonly { key: string; minutes: number }[] = [
  { key: "1h", minutes: 60 },
  { key: "4h", minutes: 4 * 60 },
  { key: "24h", minutes: 24 * 60 },
  { key: "7d", minutes: 7 * 24 * 60 },
];
