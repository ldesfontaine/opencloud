import type { Impact, Incident, IncidentEditRequest, IncidentRequest, IncidentStatus, IncidentUpdateRequest, PublicIncident, StatusState } from "../api/status";
import type { Tone } from "./tone";
import type { Translate } from "../i18n/context";

// Les pastilles de la page de statut, dérivées des mots du serveur :
// opérationnel, dégradé, panne, maintenance. Vide : rien à dire.
export const statusStateTones: Record<StatusState, Tone> = {
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

// Ouvrir un incident ou planifier une maintenance : un seul formulaire, dont
// la nature décide de l'impact, du statut et de la fenêtre qui comptent.
export type IncidentKind = "incident" | "maintenance";

export const incidentKinds: readonly IncidentKind[] = ["incident", "maintenance"];

// Ce qu'un incident neuf peut valoir : une maintenance, elle, n'a qu'un impact.
export const incidentImpacts: readonly Impact[] = ["degraded", "down"];

// Un incident s'ouvre sans être déjà résolu.
export const openingStatuses: readonly IncidentStatus[] = incidentStatuses.filter((status) => status !== "resolved");

export interface IncidentFormValues {
  kind: IncidentKind;
  title: string;
  impact: Impact;
  status: IncidentStatus;
  message: string;
  componentIDs: string[];
  startsAt: string;
  endsAt: string;
}

// Une fonction, pas une constante : la liste ne se partage pas entre deux
// formulaires.
export function emptyIncidentForm(): IncidentFormValues {
  return { kind: "incident", title: "", impact: "down", status: "investigating", message: "", componentIDs: [], startsAt: "", endsAt: "" };
}

// Une maintenance ignore l'impact et le statut tapés : elle est planifiée,
// et sa fenêtre est celle du formulaire.
export function incidentRequestOf(values: IncidentFormValues): IncidentRequest {
  const isScheduled = values.kind === "maintenance";
  return {
    title: values.title,
    impact: isScheduled ? "maintenance" : values.impact,
    status: isScheduled ? "scheduled" : values.status,
    message: values.message,
    component_ids: values.componentIDs,
    starts_at: isScheduled ? fromLocalInput(values.startsAt) : null,
    ends_at: isScheduled ? fromLocalInput(values.endsAt) : null,
  };
}

// Cocher ou décocher un composant, sans toucher à la liste reçue.
export function toggleComponent(selected: string[], id: string): string[] {
  return selected.includes(id) ? selected.filter((candidate) => candidate !== id) : [...selected, id];
}

// Une entrée du fil ne « planifie » rien : « scheduled » n'est pas proposé.
export function updateStatusesFor(impact: Impact): IncidentStatus[] {
  return statusesFor(impact).filter((status) => status !== "scheduled");
}

export interface IncidentUpdateFormValues {
  status: IncidentStatus;
  message: string;
}

// Le formulaire part du statut courant, ou du premier proposé quand le
// courant n'en fait pas partie.
export function incidentUpdateFormOf(incident: Pick<Incident, "impact" | "status">): IncidentUpdateFormValues {
  const choices = updateStatusesFor(incident.impact);
  const status = choices.includes(incident.status) ? incident.status : (choices[0] ?? incident.status);
  return { status, message: "" };
}

export function incidentUpdateRequestOf(values: IncidentUpdateFormValues): IncidentUpdateRequest {
  return { status: values.status, message: values.message };
}

// Une entrée sans message qui ne change pas le statut ne dit rien au public :
// le serveur la refuse, le bouton ne la propose pas.
export function saysNothing(values: IncidentUpdateFormValues, incident: Pick<Incident, "status">): boolean {
  return values.message.trim() === "" && values.status === incident.status;
}

export interface IncidentEditFormValues {
  title: string;
  componentIDs: string[];
  startsAt: string;
  endsAt: string;
}

export function incidentEditFormOf(incident: Incident): IncidentEditFormValues {
  return {
    title: incident.title,
    componentIDs: incident.components.map((component) => component.id),
    startsAt: toLocalInput(incident.starts_at),
    endsAt: toLocalInput(incident.ends_at),
  };
}

// La fenêtre n'appartient qu'à une maintenance : un incident garde des dates nulles.
export function incidentEditRequestOf(values: IncidentEditFormValues, impact: Impact): IncidentEditRequest {
  const isScheduled = impact === "maintenance";
  return {
    title: values.title,
    component_ids: values.componentIDs,
    starts_at: isScheduled ? fromLocalInput(values.startsAt) : null,
    ends_at: isScheduled ? fromLocalInput(values.endsAt) : null,
  };
}
