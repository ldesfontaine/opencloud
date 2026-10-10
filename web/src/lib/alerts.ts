import type { Alert, AlertKind, AlertObject, AlertSeverity, ChannelTest, Delivery, DiskThresholds, Silence } from "../api/types";
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

// Le seuil d'alert.RestartLoopThreshold : trois redémarrages dans la fenêtre.
const restartLoopThreshold = 3;

function isRestartLoop(alert: Alert): boolean {
  return (alert.kind === "service_down" || alert.kind === "service_restart") && (alert.details.count ?? 0) >= restartLoopThreshold;
}

export function describeAlert(t: Translate, alert: Alert, now: number, disks: DiskThresholds): AlertText {
  const machine = alert.machine_name !== "" ? alert.machine_name : t("alert.no_machine");
  const object = alert.object.name;
  const details = alert.details;
  // Arrêté ou relancé, un service qui plante en boucle se dit pareil :
  // c'est la même alerte, dont le type suit l'état du moment.
  if (isRestartLoop(alert)) {
    return {
      fact: t("alert.fact_service_restart_loop"),
      cause: t("alert.cause_service_restart_loop", object, machine, details.count ?? 0, details.exit_code ?? 0),
      action: t("alert.action_service_restart"),
    };
  }
  switch (alert.kind) {
    case "machine_lost":
      return lines(t, alert.kind, [], [object, formatDuration(t, details.since ? since(details.since, now) : 0)]);
    case "service_down":
      if (details.reason === "oom") {
        return {
          fact: t("alert.fact_service_down_oom"),
          cause: t("alert.cause_service_down_oom", object, machine),
          action: t("alert.action_service_down_oom"),
        };
      }
      if (details.reason === "killed") {
        return {
          fact: t("alert.fact_service_down_killed"),
          cause: t("alert.cause_service_down_killed", object, machine),
          action: t("alert.action_service_down_killed"),
        };
      }
      return lines(t, alert.kind, [], [object, machine, details.exit_code ?? 0]);
    case "service_unhealthy":
      return lines(t, alert.kind, [], [object, machine]);
    case "service_restart":
      return lines(t, alert.kind, [], [object, machine, details.exit_code ?? 0]);
    case "job_late":
      if (details.reason === "never_pinged") {
        return {
          fact: t("alert.fact_job_late_never"),
          cause: t("alert.cause_job_late_never", object, formatDuration(t, details.since ? since(details.since, now) : 0)),
          action: t("alert.action_job_late_never"),
        };
      }
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
      return lines(t, alert.kind, [details.percent ?? 0], [details.mount_point ?? object, machine, details.threshold ?? disks.attention]);
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
  // Un refus du relais porte son code SMTP : c'est lui qu'on cherche dans la doc du relais.
  if (delivery.reason.startsWith("smtp_") && delivery.code > 0) {
    return t("alert.smtp_code", t(`alert.delivery_reason_${delivery.reason}`), delivery.code);
  }
  return t(`alert.delivery_reason_${delivery.reason}`);
}

// Ce qu'a donné l'envoi d'essai d'un canal : le code reçu, ou le motif.
export function channelTestResult(t: Translate, test: ChannelTest): string {
  if (!test.ok) {
    return t("channel.test_failed", deliveryReason(t, test));
  }
  // Un e-mail n'a pas de code HTTP : le relais l'a accepté, c'est tout ce qu'on sait.
  return test.code > 0 ? t("channel.test_ok", test.code) : t("channel.test_accepted");
}

export const deliveryTones: Record<Delivery["status"], Tone> = {
  pending: "neutral",
  delivered: "ok",
  failed: "danger",
  held: "warn",
};

// Ce qu'un silence vise, en une phrase : le type, l'objet, ou les deux.
export function silenceTargets(t: Translate, silence: Silence): string {
  const kind = silence.kind !== "" ? t(`alert.kind_${silence.kind}`) : "";
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
