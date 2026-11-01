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

// Le pire des états, comme le serveur le décide (status.Worst) : une panne
// l'emporte sur une maintenance, qui l'emporte sur un dégradé. Vide quand
// aucun état ne compte.
const stateRanks: Record<StatusState, number> = { "": -1, operational: 0, degraded: 1, maintenance: 2, down: 3 };

export function worstState(states: readonly StatusState[]): StatusState {
  return states.reduce<StatusState>((worst, state) => (stateRanks[state] > stateRanks[worst] ? state : worst), "");
}

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
const incidentStatuses: readonly IncidentStatus[] = ["investigating", "identified", "monitoring", "resolved"];
const maintenanceStatuses: readonly IncidentStatus[] = ["scheduled", "in_progress", "resolved"];

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
type IncidentKind = "incident" | "maintenance";

export const incidentKinds: readonly IncidentKind[] = ["incident", "maintenance"];

// Ce qu'un incident neuf peut valoir : une maintenance, elle, n'a qu'un impact.
export const incidentImpacts: readonly Impact[] = ["degraded", "down"];

// Un incident s'ouvre sans être déjà résolu.
export const openingStatuses: readonly IncidentStatus[] = incidentStatuses.filter((status) => status !== "resolved");

interface IncidentFormValues {
  kind: IncidentKind;
  title: string;
  impact: Impact;
  status: IncidentStatus;
  message: string;
  targets: MemberChoice[];
  startsAt: string;
  endsAt: string;
}

// Une fonction, pas une constante : la liste ne se partage pas entre deux
// formulaires.
export function emptyIncidentForm(): IncidentFormValues {
  return { kind: "incident", title: "", impact: "down", status: "investigating", message: "", targets: [], startsAt: "", endsAt: "" };
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
    targets: values.targets,
    starts_at: isScheduled ? fromLocalInput(values.startsAt) : null,
    ends_at: isScheduled ? fromLocalInput(values.endsAt) : null,
  };
}

// Une entrée du fil ne « planifie » rien : « scheduled » n'est pas proposé.
export function updateStatusesFor(impact: Impact): IncidentStatus[] {
  return statusesFor(impact).filter((status) => status !== "scheduled");
}

interface IncidentUpdateFormValues {
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

interface IncidentEditFormValues {
  title: string;
  targets: MemberChoice[];
  startsAt: string;
  endsAt: string;
}

export function incidentEditFormOf(incident: Incident): IncidentEditFormValues {
  return {
    title: incident.title,
    targets: incident.targets.map((target) => ({ kind: target.kind, id: target.id })),
    startsAt: toLocalInput(incident.starts_at),
    endsAt: toLocalInput(incident.ends_at),
  };
}

// La fenêtre n'appartient qu'à une maintenance : un incident garde des dates nulles.
export function incidentEditRequestOf(values: IncidentEditFormValues, impact: Impact): IncidentEditRequest {
  const isScheduled = impact === "maintenance";
  return {
    title: values.title,
    targets: values.targets,
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

// Le projet se choisit à la création : un composant ne change plus de projet.
interface ComponentFormValues {
  projectID: string;
  name: string;
  position: string;
  members: MemberChoice[];
}

// Une fonction, pas une constante : la liste ne se partage pas entre deux
// formulaires.
export function emptyComponentForm(projectID: string): ComponentFormValues {
  return { projectID, name: "", position: "0", members: [] };
}

// Le formulaire ne garde des objets que le genre et l'identifiant : leur
// nom et leur état se relisent à chaque rendu.
export function componentFormOf(component: StatusComponent): ComponentFormValues {
  return {
    projectID: component.project_id,
    name: component.name,
    position: String(component.position),
    members: component.members.map((member) => ({ kind: member.kind, id: member.id })),
  };
}

export function componentRequestOf(values: ComponentFormValues): StatusComponentRequest {
  return { project_id: values.projectID, name: values.name, position: Number(values.position), members: values.members };
}

// Les adresses de l'administration de la disponibilité : le module, ses
// onglets, la page d'un projet publié, et les fiches qu'on y ouvre.
export const availabilityPath = "/disponibilite";
export const publicPagesPath = `${availabilityPath}/pages`;
export const incidentsPath = `${availabilityPath}/incidents`;

export function publicPagePath(projectID: string): string {
  return `${publicPagesPath}/${encodeURIComponent(projectID)}`;
}

export function newComponentPath(projectID: string): string {
  return `${availabilityPath}/composants/nouveau?project=${encodeURIComponent(projectID)}`;
}

export function componentPath(componentID: string): string {
  return `${availabilityPath}/composants/${encodeURIComponent(componentID)}`;
}

export const newIncidentPath = `${incidentsPath}/nouveau`;

export function incidentPath(incidentID: string): string {
  return `${incidentsPath}/${encodeURIComponent(incidentID)}`;
}

// Le nom d'un composant que touche un incident : avec son projet dès qu'il y
// en a plusieurs, deux projets pouvant avoir chacun leur « Site web ».
export function componentChoiceLabel(component: Pick<StatusComponent, "name" | "project_id">, projects: readonly { id: string; name: string; title: string }[]): string {
  if (projects.length < 2) {
    return component.name;
  }
  const project = projects.find((candidate) => candidate.id === component.project_id);
  return project === undefined ? component.name : `${project.title !== "" ? project.title : project.name} · ${component.name}`;
}

// Les chemins que lit une page publique, tirés de son adresse
// /statut/<slug> (barre finale admise) ; null pour une autre adresse. Le
// slug n'a besoin d'aucun échappement : minuscules, chiffres et tirets.
interface PublicStatusPaths {
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
