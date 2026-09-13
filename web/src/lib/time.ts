import type { Translate } from "../i18n/context";

const second = 1000;
const minute = 60 * second;
const hour = 60 * minute;
const day = 24 * hour;

// Un temps écoulé se tronque (« il y a 12 s »), un temps restant s'arrondit
// vers le haut (« expire dans 24 h », pas 23 h une seconde après l'émission).
export type Rounding = "elapsed" | "remaining";

// Arrondit à l'unité qui se lit : ms, s, min, h, j. Les libellés viennent
// du catalogue, comme côté serveur avant.
export function formatDuration(t: Translate, ms: number, rounding: Rounding = "elapsed"): string {
  const round = rounding === "remaining" ? Math.ceil : Math.floor;
  if (ms < 0) {
    return formatDuration(t, 0, rounding);
  }
  if (ms < second) {
    return t("time.milliseconds", round(ms));
  }
  if (ms < minute) {
    return t("time.seconds", round(ms / second));
  }
  if (ms < hour) {
    return t("time.minutes", round(ms / minute));
  }
  if (ms <= day) {
    return t("time.hours", round(ms / hour));
  }
  return t("time.days", round(ms / day));
}

export function formatRemaining(t: Translate, ms: number): string {
  return formatDuration(t, ms, "remaining");
}

export function since(at: string, now: number): number {
  return now - Date.parse(at);
}

// « vu il y a 12 s » ; jamais vu, ou à l'instant, ont leur propre mot.
export function seenAgo(t: Translate, at: string | null, now: number): string {
  if (at === null) {
    return t("machine.never_seen");
  }
  const elapsed = since(at, now);
  if (elapsed < second) {
    return t("machine.seen_now");
  }
  return t("machine.seen_ago", formatDuration(t, elapsed));
}

// « il y a 2 min » sans le verbe « vu ».
export function ago(t: Translate, at: string, now: number): string {
  const elapsed = since(at, now);
  if (elapsed < second) {
    return t("job.just_now");
  }
  return t("job.ago", formatDuration(t, elapsed));
}

// « il y a 2 min », ou « jamais » tant qu'aucun ping n'est arrivé.
export function lastPing(t: Translate, at: string | null, now: number): string {
  return at === null ? t("job.never") : ago(t, at, now);
}

// « échéance dans 4 min », ou rien quand aucune échéance ne court.
export function deadlineIn(t: Translate, at: string | null, now: number): string {
  if (at === null) {
    return t("job.no_deadline");
  }
  const remaining = Date.parse(at) - now;
  if (remaining < 0) {
    return t("job.deadline_passed");
  }
  return t("job.deadline_in", formatRemaining(t, remaining));
}

// Un instant en mono, lisible dans les deux langues, en heure locale.
export function formatClock(at: string): string {
  const date = new Date(at);
  const pad = (value: number) => String(value).padStart(2, "0");
  return (
    `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ` +
    `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
  );
}
