import type { Probe, ProbeDay, ProbeStatus } from "../api/types";
import type { Tone } from "../components/Pill";
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

export function needsAttention(probe: Pick<Probe, "status">): boolean {
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
export function uptimeOfDays(days: ProbeDay[]): { total: number; success: number } {
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

export type SegmentTone = "ok" | "warn" | "danger" | "none";

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
