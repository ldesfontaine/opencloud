import type { Impact, IncidentStatus, PublicIncident, StatusDay, StatusState } from "../api/types";
import type { Tone } from "../components/Pill";
import type { Translate } from "../i18n/context";

// Les pastilles de la page de statut, dérivées des mots du serveur :
// opérationnel, dégradé, panne, maintenance. Vide : rien à dire.
export const stateTones: Record<StatusState, Tone> = {
  operational: "ok",
  degraded: "warn",
  down: "danger",
  maintenance: "accent",
  "": "neutral",
};

// Le statut d'un incident : ce qu'il dit du travail en cours.
export const incidentTones: Record<IncidentStatus, Tone> = {
  scheduled: "neutral",
  investigating: "danger",
  identified: "warn",
  monitoring: "accent",
  in_progress: "accent",
  resolved: "ok",
};

export const impactTones: Record<Impact, Tone> = {
  degraded: "warn",
  down: "danger",
  maintenance: "accent",
};

// Un incident se planifie ou s'analyse : les statuts qui ont un sens pour
// chaque nature, dans l'ordre où l'opérateur les parcourt.
export const incidentStatuses: readonly IncidentStatus[] = ["investigating", "identified", "monitoring", "resolved"];
export const maintenanceStatuses: readonly IncidentStatus[] = ["scheduled", "in_progress", "resolved"];

export function statusesFor(impact: Impact): readonly IncidentStatus[] {
  return impact === "maintenance" ? maintenanceStatuses : incidentStatuses;
}

// Une maintenance est un incident d'impact « maintenance » avec une fenêtre.
export function isMaintenance(incident: Pick<PublicIncident, "impact">): boolean {
  return incident.impact === "maintenance";
}

export function isOpen(incident: Pick<PublicIncident, "status">): boolean {
  return incident.status !== "resolved";
}

// La disponibilité sur les jours d'un composant : les comptes s'additionnent.
export function uptimeOf(days: StatusDay[]): { total: number; success: number } {
  let total = 0;
  let success = 0;
  for (const day of days) {
    total += day.total;
    success += day.success;
  }
  return { total, success };
}

// Un pourcentage pour un visiteur : deux décimales, la virgule en français,
// et jamais « 100 » pour une disponibilité entamée.
export function formatPercent(total: number, success: number, language: string): string | null {
  if (total <= 0) {
    return null;
  }
  const percent = (success / total) * 100;
  const rounded = percent < 100 && percent > 99.995 ? 99.99 : percent;
  return rounded.toLocaleString(language, { minimumFractionDigits: 0, maximumFractionDigits: 2 });
}

// Le sous-titre de l'onglet Incidents : rien, ou le compte et les ouverts.
export function incidentsSubtitle(t: Translate, total: number, open: number): string {
  if (total === 0) {
    return t("incident.subtitle_none");
  }
  if (open === 0) {
    return t("incident.subtitle_ok", total);
  }
  return t("incident.subtitle_open", total, open);
}

// Un champ datetime-local parle en heure locale sans fuseau ; l'API en
// instants ISO. Les deux conversions vivent ici, et nulle part ailleurs.
export function toLocalInput(iso: string | null): string {
  if (iso === null) {
    return "";
  }
  const date = new Date(iso);
  const pad = (value: number) => String(value).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

export function fromLocalInput(value: string): string | null {
  if (value === "") {
    return null;
  }
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? null : parsed.toISOString();
}
