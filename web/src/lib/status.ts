import type {
  Impact,
  Incident,
  IncidentEditRequest,
  IncidentRequest,
  IncidentStatus,
  IncidentUpdateRequest,
  MemberKind,
  PublicIncident,
  StatusComponent,
  StatusComponentRequest,
  StatusPage,
  StatusPageRequest,
  StatusState,
} from "../api/status";
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

// Les genres d'objets qu'un composant rattache, dans l'ordre du sélecteur.
export const memberKinds: readonly MemberKind[] = ["machine", "service", "heartbeat", "probe"];

// Un objet rattaché, tel que le formulaire le garde et le renvoie.
export interface MemberChoice {
  kind: MemberKind;
  id: string;
}

// La page se choisit à la création : un composant ne change plus de page.
export interface ComponentFormValues {
  pageID: string;
  name: string;
  position: string;
  members: MemberChoice[];
}

// Une fonction, pas une constante : la liste ne se partage pas entre deux
// formulaires.
export function emptyComponentForm(pageID: string): ComponentFormValues {
  return { pageID, name: "", position: "0", members: [] };
}

// Le formulaire ne garde des objets que le genre et l'identifiant : leur
// nom et leur état se relisent à chaque rendu.
export function componentFormOf(component: StatusComponent): ComponentFormValues {
  return {
    pageID: component.page_id,
    name: component.name,
    position: String(component.position),
    members: component.members.map((member) => ({ kind: member.kind, id: member.id })),
  };
}

export function componentRequestOf(values: ComponentFormValues): StatusComponentRequest {
  return { page_id: values.pageID, name: values.name, position: Number(values.position), members: values.members };
}

// Le formulaire d'une page est aussi le corps de son enregistrement :
// l'adresse, le titre, l'annonce, la langue, le projet (vide : aucun).
export const emptyStatusPageForm: StatusPageRequest = { slug: "", title: "", announcement: "", language: "", project_id: "" };

export function statusPageFormOf(page: StatusPage): StatusPageRequest {
  return { slug: page.slug, title: page.title, announcement: page.announcement, language: page.language, project_id: page.project_id };
}

// Le nom d'une page dans une liste : son titre, sinon son adresse.
export function pageName(page: Pick<StatusPage, "title" | "slug">): string {
  return page.title !== "" ? page.title : page.slug;
}

// Le nom d'un composant à cocher pour un incident : avec sa page dès qu'il y
// en a plusieurs, deux pages pouvant avoir chacune leur « Site web ».
export function componentChoiceLabel(component: Pick<StatusComponent, "name" | "page_id">, pages: readonly StatusPage[]): string {
  if (pages.length < 2) {
    return component.name;
  }
  const page = pages.find((candidate) => candidate.id === component.page_id);
  return page === undefined ? component.name : `${pageName(page)} · ${component.name}`;
}

// La page dont l'écran des composants montre les composants : celle que
// l'adresse demande si elle existe, sinon la première ; aucune sans page.
export function chosenPageID(pages: readonly Pick<StatusPage, "id">[], requested: string | null): string | null {
  if (requested !== null && pages.some((page) => page.id === requested)) {
    return requested;
  }
  return pages[0]?.id ?? null;
}

// Les chemins que lit une page publique, tirés de son adresse
// /statut/<slug> (barre finale admise) ; null pour une autre adresse. Le
// slug n'a besoin d'aucun échappement : minuscules, chiffres et tirets.
export interface PublicStatusPaths {
  status: string;
  catalog: string;
  events: string;
}

const publicPagePattern = /^\/statut\/([a-z0-9][a-z0-9-]*)\/?$/;

export function publicStatusPaths(pathname: string): PublicStatusPaths | null {
  const match = publicPagePattern.exec(pathname);
  if (match === null) {
    return null;
  }
  const base = `/statut/${match[1]}/api`;
  return { status: `${base}/status`, catalog: `${base}/i18n`, events: `${base}/events` };
}
