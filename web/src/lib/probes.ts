import type { Container } from "../api/containers";
import type { DeclaredService } from "../api/directory";
import type { Probe, ProbeDay, ProbeKind, ProbeRequest, ProbeStatus } from "../api/probes";
import { isPublicIP } from "./containers";
import type { SegmentTone, Tone } from "./tone";
import type { Translate } from "../i18n/context";

// Les pastilles de la direction artistique. Dégradé est une attention, pas
// un danger : la cible répond, c'est sa chaîne de certificats qui déplaît.
export const probeTones: Record<ProbeStatus, Tone> = {
  new: "neutral",
  up: "ok",
  degraded: "warn",
  down: "danger",
  paused: "neutral",
};

export const attentionProbeStatuses: ReadonlySet<ProbeStatus> = new Set<ProbeStatus>(["down"]);

export function probeNeedsAttention(probe: Pick<Probe, "status">): boolean {
  return attentionProbeStatuses.has(probe.status);
}

// La disponibilité d'une fenêtre : rien tant qu'aucun essai n'y est tombé,
// jamais un zéro qui ferait croire à une panne.
export function uptimePercent(total: number, success: number): number | null {
  if (total <= 0) {
    return null;
  }
  return (success / total) * 100;
}

// La disponibilité d'une suite de jours : les comptes s'additionnent, c'est
// pour cela que l'agrégat en garde plutôt qu'un pourcentage.
export function uptimeOfDays(days: Pick<ProbeDay, "total" | "success">[]): { total: number; success: number } {
  let total = 0;
  let success = 0;
  for (const day of days) {
    total += day.total;
    success += day.success;
  }
  return { total, success };
}

// Les jours d'une barre, du plus ancien à aujourd'hui, en heure UTC.
export function dayKeys(span: number, now: number): string[] {
  const today = new Date(now);
  const keys: string[] = [];
  for (let back = span - 1; back >= 0; back -= 1) {
    const day = new Date(Date.UTC(today.getUTCFullYear(), today.getUTCMonth(), today.getUTCDate() - back));
    keys.push(day.toISOString().slice(0, 10));
  }
  return keys;
}

// Le ton d'un jour : un échec l'emporte, puis un essai dégradé, sinon
// tout va bien ; sans essai, rien à dire.
export function dayTone(day: Pick<ProbeDay, "total" | "success" | "degraded"> | undefined): SegmentTone {
  if (day === undefined || day.total === 0) {
    return "none";
  }
  if (day.success < day.total) {
    return "danger";
  }
  return day.degraded > 0 ? "warn" : "ok";
}

// Le sous-titre de la page : rien, une, plusieurs, et ce qui est hors ligne.
export function probesSubtitle(t: Translate, total: number, attention: number): string {
  if (total === 0) {
    return t("probes.subtitle_none");
  }
  if (total === 1) {
    return t(attention === 0 ? "probes.subtitle_one" : "probes.subtitle_one_attention");
  }
  if (attention === 0) {
    return t("probes.subtitle_ok", total);
  }
  return t("probes.subtitle_attention", total, attention);
}

// Les natures d'une sonde, dans l'ordre du formulaire.
export const probeKinds: readonly ProbeKind[] = ["http", "tcp"];

// Le formulaire d'une sonde, champ par champ, tel que l'opérateur le tape :
// des textes, que probeRequestOf convertit pour le serveur.
export interface ProbeFormValues {
  name: string;
  kind: ProbeKind;
  target: string;
  machineID: string;
  // Le service déclaré que la sonde vise ; vide : aucun.
  serviceID: string;
  interval: string;
  timeout: string;
  failure: string;
  recovery: string;
  method: string;
  expectedStatus: string;
  expectedBody: string;
  redirects: "yes" | "no";
  // Une sonde TCP peut faire une poignée de main plutôt qu'une simple
  // connexion : c'est ce qui donne son certificat à un port chiffré qui ne
  // parle pas HTTP, SMTP ou IMAP.
  useTLS: "yes" | "no";
}

// Les valeurs d'une sonde neuve, celles du serveur à défaut de réponse.
export const emptyProbeForm: ProbeFormValues = {
  name: "",
  kind: "http",
  target: "",
  machineID: "",
  serviceID: "",
  interval: "60",
  timeout: "10",
  failure: "3",
  recovery: "2",
  method: "GET",
  expectedStatus: "2xx",
  expectedBody: "",
  redirects: "yes",
  useTLS: "no",
};

// Le formulaire d'édition part de la sonde telle que le serveur la garde.
// Une sonde TCP n'a ni méthode ni code attendu : le formulaire reprend les
// valeurs par défaut, qu'un passage à HTTP retrouverait.
export function probeFormOf(probe: Probe): ProbeFormValues {
  const isHTTP = probe.kind === "http";
  return {
    name: probe.name,
    kind: probe.kind,
    target: probe.target,
    machineID: probe.machine_id,
    serviceID: probe.service_id,
    interval: String(probe.interval_seconds),
    timeout: String(probe.timeout_seconds),
    failure: String(probe.failure_threshold),
    recovery: String(probe.recovery_threshold),
    method: isHTTP ? probe.method : emptyProbeForm.method,
    expectedStatus: isHTTP ? probe.expected_status : emptyProbeForm.expectedStatus,
    expectedBody: probe.expected_body,
    redirects: probe.follow_redirects ? "yes" : "no",
    useTLS: probe.tls ? "yes" : "no",
  };
}

// Le corps de la création comme de l'édition : le serveur remplace la
// définition entière. Une sonde TCP envoie les champs HTTP vides, que le
// serveur ne retient pas.
export function probeRequestOf(values: ProbeFormValues): ProbeRequest {
  const isHTTP = values.kind === "http";
  return {
    name: values.name,
    kind: values.kind,
    target: values.target,
    machine_id: values.machineID,
    service_id: values.serviceID,
    interval_seconds: Number(values.interval),
    timeout_seconds: Number(values.timeout),
    failure_threshold: Number(values.failure),
    recovery_threshold: Number(values.recovery),
    method: isHTTP ? values.method : "",
    expected_status: isHTTP ? values.expectedStatus : "",
    expected_body: isHTTP ? values.expectedBody : "",
    follow_redirects: isHTTP && values.redirects === "yes",
    tls: values.kind === "tcp" && values.useTLS === "yes",
  };
}

// Venue d'un service déclaré, la sonde en prend le nom. Venue d'un de ses
// conteneurs, la machine qui le porte sonde, port publié ou non : c'est la
// vue depuis l'intérieur, qui marche derrière un NAT comme ailleurs. Avec un
// port publié, la sonde le vise ; sans port, il n'y a pas de cible à deviner.
export function probeSuggestionOf(
  service: Pick<DeclaredService, "name" | "title">,
  carrier: Pick<Container, "machine_id" | "ports"> | undefined,
): Partial<ProbeFormValues> {
  const suggestion: Partial<ProbeFormValues> = { name: service.title !== "" ? service.title : service.name };
  if (carrier === undefined) {
    return suggestion;
  }
  suggestion.machineID = carrier.machine_id;
  const port = carrier.ports[0];
  if (port !== undefined) {
    const host = isPublicIP(port.ip) ? "127.0.0.1" : port.ip;
    suggestion.target = `http://${host}:${String(port.host_port)}/`;
  }
  return suggestion;
}
