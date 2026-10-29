import type { Alert, AlertKind, AlertObject, AlertSeverity, Channel, ChannelFormat, ChannelRequest, ChannelTest, Delivery, Mute, MuteRequest, MuteTargetKind } from "../api/alerts";
import type { DiskThresholds } from "../api/session";
import type { Tone } from "./tone";
import type { Translate } from "../i18n/context";
import { daysUntil } from "./certificates";
import { probePath } from "./probes";
import { fromLocalInput } from "./status";
import { formatClock, formatDuration, since } from "./time";

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
  // Arrêté ou relancé, un conteneur qui plante en boucle se dit pareil :
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
    case "agent_rolled_back":
      return lines(t, alert.kind, [], [machine, details.target ?? "", t(`alert.rollback_cause_${details.reason ?? "timeout"}`)]);
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

// La plus urgente des alertes ouvertes : une que personne n'a acquittée avant
// une acquittée, puis la plus grave, puis la plus récente. null : aucune
// ouverte. C'est ce que la tuile Alertes de la vue d'ensemble montre.
export function mostUrgentAlert(alerts: Alert[]): Alert | null {
  let best: Alert | null = null;
  for (const alert of alerts) {
    if (alert.status !== "open") {
      continue;
    }
    if (best === null || urgencyOf(alert) > urgencyOf(best) || (urgencyOf(alert) === urgencyOf(best) && Date.parse(alert.opened_at) > Date.parse(best.opened_at))) {
      best = alert;
    }
  }
  return best;
}

// Non acquittée : 2, grave : 1 ; la gravité ne vient qu'après l'acquittement.
function urgencyOf(alert: Alert): number {
  return (alert.acknowledged_at === null ? 2 : 0) + (alert.severity === "danger" ? 1 : 0);
}

// L'état d'une alerte tel que la pastille le dit.
export type AlertState = "open" | "acknowledged" | "resolved";

export function stateOf(alert: Pick<Alert, "status" | "acknowledged_at">): AlertState {
  if (alert.status === "resolved") {
    return "resolved";
  }
  return alert.acknowledged_at !== null ? "acknowledged" : "open";
}

export const alertStateTones: Record<AlertState, Tone> = {
  open: "danger",
  acknowledged: "accent",
  resolved: "ok",
};

// Où mène un objet : sa fiche ; un volume, sa machine.
export function objectPath(object: AlertObject, machineID: string): string | null {
  switch (object.kind) {
    case "machine":
      return `/machines/${object.id}`;
    case "container":
      return `/services/conteneurs/${object.id}`;
    case "heartbeat":
      return `/taches/${object.id}`;
    case "probe":
      return probePath(object.id);
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

// L'échec d'une livraison : son motif, et l'heure de l'essai suivant s'il y
// en a un.
export function deliveryFailure(t: Translate, delivery: Pick<Delivery, "reason" | "code" | "next_attempt_at">): string {
  const reason = deliveryReason(t, delivery);
  if (delivery.next_attempt_at === null) {
    return reason;
  }
  return `${reason} · ${t("alert.delivery_retry", formatClock(delivery.next_attempt_at))}`;
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
  muted: "neutral",
};

// Ce qu'un silence vise, en une phrase : le type, l'objet, les deux, ou tout.
export function muteTargets(t: Translate, mute: Pick<Mute, "kind" | "target">): string {
  const kind = mute.kind !== "" ? t(`alert.kind_${mute.kind}`) : "";
  const object = mute.target.kind !== "" ? mute.target.name : "";
  if (kind !== "" && object !== "") {
    return t("silence.targets_both", kind, object);
  }
  if (kind !== "") {
    return t("silence.targets_kind", kind);
  }
  if (object !== "") {
    return t("silence.targets_object", object);
  }
  return t("silence.targets_all");
}

// L'état d'un silence tel que la pastille le dit.
export type MuteState = "active" | "planned" | "expired";

export function muteStateOf(mute: Pick<Mute, "active" | "starts_at">, now: number): MuteState {
  if (mute.active) {
    return "active";
  }
  return Date.parse(mute.starts_at) > now ? "planned" : "expired";
}

// Les durées qu'un silence propose, en minutes.
export const silenceDurations: readonly { key: string; minutes: number }[] = [
  { key: "1h", minutes: 60 },
  { key: "4h", minutes: 4 * 60 },
  { key: "24h", minutes: 24 * 60 },
  { key: "7d", minutes: 7 * 24 * 60 },
];

// Le formulaire d'un silence, tel que l'opérateur le règle. La page le
// pré-remplit depuis l'adresse (type, cible) quand elle vient d'une alerte ;
// targetName ne sert qu'à montrer un conteneur non déclaré, qu'aucune liste
// ne propose. startsAt est la valeur d'un champ datetime-local ; vide : tout
// de suite.
export interface MuteFormValues {
  kind: AlertKind | "";
  targetKind: MuteTargetKind;
  targetID: string;
  targetName: string;
  startsAt: string;
  minutes: number;
  reason: string;
  announce: boolean;
  publicTitle: string;
}

const defaultSilenceMinutes = 60;

export const emptyMuteForm: MuteFormValues = {
  kind: "",
  targetKind: "",
  targetID: "",
  targetName: "",
  startsAt: "",
  minutes: defaultSilenceMinutes,
  reason: "",
  announce: false,
  publicTitle: "",
};

// Seul un objet du répertoire peut s'annoncer : une page de statut n'en
// montre pas d'autres.
export function canAnnounce(values: Pick<MuteFormValues, "targetKind" | "targetID">): boolean {
  return values.targetID !== "" && announceableKinds.includes(values.targetKind);
}

const announceableKinds: readonly MuteTargetKind[] = ["machine", "service", "probe", "heartbeat"];

// Un genre sans objet choisi ne vise rien : le serveur reçoit « tout ».
export function muteRequestOf(values: MuteFormValues): MuteRequest {
  const announced = values.announce && canAnnounce(values);
  return {
    kind: values.kind,
    target_kind: values.targetID !== "" ? values.targetKind : "",
    target_id: values.targetID,
    reason: values.reason,
    starts_at: fromLocalInput(values.startsAt),
    duration_minutes: values.minutes,
    announce: announced,
    public_title: announced ? values.publicTitle : "",
  };
}

// Ce qu'on fait taire depuis une alerte : son objet, ou ce qui le porte. Un
// volume se tait par le disque de sa machine, un conteneur par son service
// déclaré quand il en a un.
export function muteTargetOf(alert: Pick<Alert, "kind" | "object" | "machine_id" | "machine_name" | "service_id">): { kind: AlertKind | ""; targetKind: MuteTargetKind; targetID: string; targetName: string } {
  switch (alert.object.kind) {
    case "volume":
      return { kind: alert.kind, targetKind: "machine", targetID: alert.machine_id, targetName: alert.machine_name };
    case "container":
      if (alert.service_id !== "") {
        return { kind: alert.kind, targetKind: "service", targetID: alert.service_id, targetName: "" };
      }
      return { kind: alert.kind, targetKind: "container", targetID: alert.object.id, targetName: alert.object.name };
  }
  return { kind: alert.kind, targetKind: alert.object.kind, targetID: alert.object.id, targetName: alert.object.name };
}

// L'adresse du formulaire de silence, pré-rempli depuis l'alerte.
export function mutePathOf(alert: Pick<Alert, "kind" | "object" | "machine_id" | "machine_name" | "service_id">): string {
  const target = muteTargetOf(alert);
  const query = new URLSearchParams({ kind: target.kind, target_kind: target.targetKind, target_id: target.targetID, target_name: target.targetName });
  return `/alertes/silences/nouveau?${query.toString()}`;
}

// Les listes fermées du formulaire d'un canal, dans son ordre.
export const channelFormats: readonly ChannelFormat[] = ["json", "text", "discord", "slack", "email", "matrix"];
export const alertSeverities: readonly AlertSeverity[] = ["attention", "danger"];

// Le formulaire d'un canal. Le secret (ou le jeton d'un bot Matrix) ne
// revient jamais du serveur : le champ part vide, et vide il garde celui
// qui est enregistré ; clearSecret le retire.
export interface ChannelFormValues {
  name: string;
  url: string;
  format: ChannelFormat;
  destination: string;
  secret: string;
  clearSecret: boolean;
  minSeverity: AlertSeverity;
  notifyResolve: boolean;
  receivesAudit: boolean;
  enabled: boolean;
}

export const emptyChannelForm: ChannelFormValues = {
  name: "",
  url: "",
  format: "json",
  destination: "",
  secret: "",
  clearSecret: false,
  minSeverity: "attention",
  notifyResolve: true,
  receivesAudit: false,
  enabled: true,
};

// Un webhook n'a pas de destination : le champ part vide.
export function channelFormOf(channel: Channel): ChannelFormValues {
  return {
    name: channel.name,
    url: channel.url,
    format: channel.format,
    destination: channel.destination ?? "",
    secret: "",
    clearSecret: false,
    minSeverity: channel.min_severity,
    notifyResolve: channel.notify_resolve,
    receivesAudit: channel.receives_audit,
    enabled: channel.enabled,
  };
}

export function channelRequestOf(values: ChannelFormValues): ChannelRequest {
  return {
    name: values.name,
    url: values.url,
    format: values.format,
    destination: values.destination,
    secret: values.secret,
    clear_secret: values.clearSecret,
    min_severity: values.minSeverity,
    notify_resolve: values.notifyResolve,
    receives_audit: values.receivesAudit,
    enabled: values.enabled,
  };
}
