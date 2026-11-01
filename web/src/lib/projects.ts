import type { Project, ProjectRequest } from "../api/directory";
import type { Translate } from "../i18n/context";

// Un projet et sa page publique, côté formulaire. Le formulaire est aussi le
// corps de l'enregistrement : le serveur remplace le projet entier, sa
// publication comprise, et un projet non publié n'envoie pas de page.

export interface ProjectFormValues {
  name: string;
  title: string;
  published: boolean;
  slug: string;
  announcement: string;
  language: string;
}

// La langue d'une page neuve est celle de l'interface.
export function emptyProjectForm(language: string): ProjectFormValues {
  return { name: "", title: "", published: false, slug: "", announcement: "", language };
}

export function projectFormOf(project: Project): ProjectFormValues {
  const publication = project.publication;
  return {
    name: project.name,
    title: project.title,
    published: publication !== null,
    slug: publication?.slug ?? "",
    announcement: publication?.announcement ?? "",
    language: publication?.language ?? "",
  };
}

export function projectRequestOf(values: ProjectFormValues): ProjectRequest {
  return {
    name: values.name,
    title: values.title,
    publication: values.published ? { slug: values.slug, announcement: values.announcement, language: values.language } : null,
  };
}

// L'adresse que le serveur donnerait à un projet qu'on publie : son nom, dont
// les tirets bas deviennent des tirets (un slug n'en admet pas).
export function slugSuggestionOf(name: string): string {
  return name.trim().replace(/_/g, "-");
}

// Le nom d'un projet dans une liste : son titre, sinon son nom.
export function projectLabel(project: Pick<Project, "name" | "title">): string {
  return project.title !== "" ? project.title : project.name;
}

// L'adresse publique, sans l'hôte : c'est le chemin qui distingue les pages.
export function publicAddressOf(slug: string): string {
  return `/statut/${slug}`;
}

export function projectsSubtitle(t: Translate, count: number): string {
  if (count === 0) {
    return t("project.subtitle_none");
  }
  return count === 1 ? t("project.subtitle_one") : t("project.subtitle", count);
}
