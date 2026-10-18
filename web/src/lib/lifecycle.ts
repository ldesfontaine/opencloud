import type { Lifecycle, LifecycleSettings, LifecycleStatus, NewerCycle } from "../api/types";
import type { Tone } from "../components/Pill";
import type { Translate } from "../i18n/context";
import { ago } from "./time";

// Une branche en fin de support demande l'opérateur ; ce qu'on ne sait pas
// reste neutre.
export const lifecycleTones: Record<LifecycleStatus, Tone> = {
  maintained: "ok",
  eol: "warn",
  unknown_product: "neutral",
  unknown_version: "neutral",
  pending: "neutral",
};

// Un jour de la source, « 2028-11-09 », écrit en toutes lettres dans la
// langue de l'interface. Le jour est un jour, pas un instant : on le lit
// en UTC pour qu'il ne glisse pas d'un fuseau à l'autre.
export function formatDay(day: string, language: string): string {
  const date = new Date(`${day}T00:00:00Z`);
  if (Number.isNaN(date.getTime())) {
    return day;
  }
  return new Intl.DateTimeFormat(language, { dateStyle: "long", timeZone: "UTC" }).format(date);
}

// Ce dont on dit le support : l'image d'un conteneur, ou le système d'une
// machine. Ce qu'on ne sait pas ne se dit pas avec les mêmes mots.
export type Subject = "image" | "system";

// La phrase du support : la branche et jusqu'à quand, ou ce qu'on ne sait
// pas dire et pourquoi.
export function lifecycleText(t: Translate, lifecycle: Lifecycle, language: string, subject: Subject): string {
  const isSystem = subject === "system";
  switch (lifecycle.status) {
    case "maintained":
      return lifecycle.eol_from === ""
        ? t("lifecycle.maintained", lifecycle.product, lifecycle.cycle)
        : t("lifecycle.maintained_until", lifecycle.product, lifecycle.cycle, formatDay(lifecycle.eol_from, language));
    case "eol":
      return lifecycle.eol_from === ""
        ? t("lifecycle.eol", lifecycle.product, lifecycle.cycle)
        : t("lifecycle.eol_since", lifecycle.product, lifecycle.cycle, formatDay(lifecycle.eol_from, language));
    case "unknown_version":
      return isSystem ? t("lifecycle.unknown_version_system", lifecycle.product) : t("lifecycle.unknown_version", lifecycle.product);
    case "unknown_product":
      return isSystem ? t("lifecycle.unknown_system") : t("lifecycle.unknown_product");
    case "pending":
      return isSystem ? t("lifecycle.pending_system") : t("lifecycle.pending");
  }
}

// Une branche maintenue au-dessus de celle qui tourne : sa dernière
// version, et jusqu'à quand elle est maintenue.
export function newerLine(t: Translate, newer: NewerCycle, language: string): string {
  if (newer.latest !== "" && newer.eol_from !== "") {
    return t("lifecycle.newer_full", newer.cycle, newer.latest, formatDay(newer.eol_from, language));
  }
  if (newer.latest !== "") {
    return t("lifecycle.newer_latest", newer.cycle, newer.latest);
  }
  if (newer.eol_from !== "") {
    return t("lifecycle.newer_until", newer.cycle, formatDay(newer.eol_from, language));
  }
  return newer.cycle;
}

// Ce que Paramètres dit de la dernière lecture de la source.
export function readingText(t: Translate, settings: LifecycleSettings, now: number): string {
  if (settings.is_failing && settings.last_attempt_at !== null) {
    return settings.fetched_at === null
      ? t("lifecycle.reading_failed_first", ago(t, settings.last_attempt_at, now))
      : t("lifecycle.reading_failed", ago(t, settings.last_attempt_at, now), ago(t, settings.fetched_at, now));
  }
  if (settings.fetched_at === null) {
    return t("lifecycle.reading_none");
  }
  return t("lifecycle.reading_done", ago(t, settings.fetched_at, now), settings.products);
}
