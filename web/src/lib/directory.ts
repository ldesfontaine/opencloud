import type { Capability, DeclaredService, Nature, Origin, Project, ProposalResponse, ServiceLinks, ServiceRequest, ServiceUnit, UpdatePolicy } from "../api/directory";
import type { Translate } from "../i18n/context";

// Les listes fermées du serveur, dans l'ordre des formulaires. Un test Go
// vérifie que chaque valeur a son libellé dans les deux langues.
export const natures: readonly Nature[] = ["application", "infra"];
export const origins: readonly Origin[] = ["compose", "container", "binary"];
export const capabilities: readonly Capability[] = ["sso", "proxy", "tls", "backup-target"];
export const updatePolicies: readonly UpdatePolicy[] = ["watch", "hold", "ignore"];

// Une unité telle que l'opérateur la tape : des textes. Les ports se
// saisissent sur une ligne, séparés par des virgules ou des espaces.
export interface UnitFormValues {
  // Vide pour une unité nouvelle ; à l'édition, celui de l'unité gardée.
  id: string;
  name: string;
  image: string;
  version: string;
  digest: string;
  containerName: string;
  systemdUnit: string;
  ports: string;
  // Ce que l'opérateur veut des mises à jour de l'unité ; un binaire n'en a pas.
  updatePolicy: UpdatePolicy;
}

export interface ServiceFormValues {
  machineID: string;
  name: string;
  title: string;
  nature: Nature;
  origin: Origin;
  composeProject: string;
  publicURL: string;
  provides: Capability[];
  units: UnitFormValues[];
  projectIDs: string[];
}

export function emptyUnitForm(): UnitFormValues {
  return { id: "", name: "", image: "", version: "", digest: "", containerName: "", systemdUnit: "", ports: "", updatePolicy: "watch" };
}

// Un service neuf : une application Compose et sa première unité vide. Une
// fonction, pas une constante : les listes ne se partagent pas entre deux
// formulaires.
export function emptyServiceForm(): ServiceFormValues {
  return {
    machineID: "",
    name: "",
    title: "",
    nature: "application",
    origin: "compose",
    composeProject: "",
    publicURL: "",
    provides: [],
    units: [emptyUnitForm()],
    projectIDs: [],
  };
}

export function unitFormOf(unit: ServiceUnit): UnitFormValues {
  return {
    id: unit.id,
    name: unit.name,
    image: unit.image,
    version: unit.version,
    digest: unit.digest,
    containerName: unit.container_name,
    systemdUnit: unit.systemd_unit,
    ports: formatUnitPorts(unit.ports),
    updatePolicy: unit.update_policy,
  };
}

// Le formulaire d'édition part du service tel que le serveur le garde.
export function serviceFormOf(service: DeclaredService): ServiceFormValues {
  return {
    machineID: service.machine_id,
    name: service.name,
    title: service.title,
    nature: service.nature,
    origin: service.origin,
    composeProject: service.compose_project,
    publicURL: service.public_url,
    provides: [...service.provides],
    units: service.units.map(unitFormOf),
    projectIDs: [...service.project_ids],
  };
}

// Le formulaire part de ce que le serveur propose pour « Adopter » : la
// forme du corps de la création, que le formulaire reprend sans la
// reconstruire. Les unités gardées d'un service déjà déclaré y portent leur
// identifiant, les ajoutées n'en ont pas.
export function serviceFormOfDefinition(definition: ProposalResponse["definition"]): ServiceFormValues {
  return {
    machineID: definition.machine_id,
    name: definition.name,
    title: definition.title,
    nature: definition.nature,
    origin: definition.origin,
    composeProject: definition.compose_project,
    publicURL: definition.public_url,
    provides: [...definition.provides],
    units: definition.units.map((unit) => ({
      id: unit.id,
      name: unit.name,
      image: unit.image,
      version: unit.version,
      digest: unit.digest,
      containerName: unit.container_name,
      systemdUnit: unit.systemd_unit,
      ports: formatUnitPorts(unit.ports),
      // Une unité ajoutée par la proposition n'en porte pas : elle se suit.
      updatePolicy: unit.update_policy ?? "watch",
    })),
    projectIDs: [...definition.project_ids],
  };
}

// Les valeurs dont part le formulaire d'un service, ou null tant qu'il faut
// attendre. En édition : le service tel qu'il est gardé, ou la proposition
// d'adoption quand elle le vise (elle y ajoute les unités qui manquent), une
// fois la proposition arrivée ou refusée. En création : la proposition, si
// elle déclare un service neuf ; une proposition qui vise un service déjà
// déclaré redirige vers son édition, la page s'en charge.
export function serviceFormStart(
  editedID: string | undefined,
  service: DeclaredService | null,
  proposal: ProposalResponse | null,
  awaitingProposal: boolean,
): ServiceFormValues | null {
  if (editedID !== undefined) {
    if (service === null || awaitingProposal) {
      return null;
    }
    // Une proposition pour un autre service n'est pas la nôtre : le service
    // tel qu'il est gardé fait foi.
    if (proposal !== null && proposal.service_id === editedID) {
      return serviceFormOfDefinition(proposal.definition);
    }
    return serviceFormOf(service);
  }
  if (proposal === null || proposal.service_id !== "") {
    return null;
  }
  return serviceFormOfDefinition(proposal.definition);
}

export function parsePorts(text: string): string[] {
  return text.split(/[\s,]+/).filter((port) => port !== "");
}

export function formatUnitPorts(ports: readonly string[]): string {
  return ports.join(", ");
}

// Le formulaire d'un projet est aussi le corps de sa création comme de son
// édition : un nom et un titre, rien de ce que le serveur tient seul.
export interface ProjectFormValues {
  name: string;
  title: string;
}

export const emptyProjectForm: ProjectFormValues = { name: "", title: "" };

export function projectFormOf(project: Project): ProjectFormValues {
  return { name: project.name, title: project.title };
}

// Un nom de projet par identifiant, pour dire à quels projets appartient
// chaque service sans relire la liste ligne par ligne.
export function projectNames(projects: Project[]): Map<string, string> {
  return new Map(projects.map((project) => [project.id, project.name]));
}

// Le corps de la création comme de l'édition : le serveur remplace la
// définition entière et refuse ce qui n'a pas d'objet pour l'origine. Le
// formulaire garde ce que l'opérateur a tapé en changeant d'origine ; la
// requête, elle, ne porte que ce qui compte.
export function serviceRequestOf(values: ServiceFormValues): ServiceRequest {
  const isCompose = values.origin === "compose";
  const isContainer = values.origin === "container";
  const isBinary = values.origin === "binary";
  return {
    machine_id: values.machineID,
    name: values.name,
    title: values.title,
    nature: values.nature,
    origin: values.origin,
    compose_project: isCompose ? values.composeProject : "",
    public_url: values.publicURL,
    provides: values.nature === "infra" ? values.provides : [],
    units: values.units.map((unit) => ({
      id: unit.id,
      name: unit.name,
      image: isBinary ? "" : unit.image,
      version: unit.version,
      digest: isBinary ? "" : unit.digest,
      container_name: isContainer ? unit.containerName : "",
      systemd_unit: isBinary ? unit.systemdUnit : "",
      ports: parsePorts(unit.ports),
      // La politique ne vaut que pour un conteneur : un binaire se suit.
      update_policy: isBinary ? "watch" : unit.updatePolicy,
    })),
    project_ids: values.projectIDs,
  };
}

export function unitKeyOf(unit: ServiceUnit, origin: Origin): string {
  switch (origin) {
    case "compose":
      return unit.name;
    case "container":
      return unit.container_name;
    case "binary":
      return unit.systemd_unit;
  }
}

export function toggled<Value>(values: readonly Value[], value: Value): Value[] {
  return values.includes(value) ? values.filter((candidate) => candidate !== value) : [...values, value];
}

export function directorySubtitle(t: Translate, count: number): string {
  if (count === 0) {
    return t("directory.subtitle_none");
  }
  return count === 1 ? t("directory.subtitle_one") : t("directory.subtitle", count);
}

export function projectsSubtitle(t: Translate, count: number): string {
  if (count === 0) {
    return t("project.subtitle_none");
  }
  return count === 1 ? t("project.subtitle_one") : t("project.subtitle", count);
}

export function hasLinks(links: ServiceLinks): boolean {
  return links.probes.length > 0 || links.components.length > 0;
}
