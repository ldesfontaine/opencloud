import { describe, expect, it } from "vitest";

import type { Project } from "../api/directory";
import { chosenProjectID, emptyProjectForm, projectFormOf, projectLabel, projectRequestOf, projectsSubtitle, publicAddressOf, slugSuggestionOf } from "./projects";

const published: Project = {
  id: "prj_1",
  name: "client-a",
  title: "Client A",
  service_ids: ["svc_1"],
  publication: { slug: "client-a", announcement: "Travaux jeudi", language: "en", public_url: "https://exemple.fr/statut/client-a", published_at: "2026-10-18T09:00:00Z" },
  created_at: "2026-10-18T09:00:00Z",
  updated_at: "2026-10-18T09:00:00Z",
};
const draft: Project = { ...published, id: "prj_2", name: "brouillon", title: "", publication: null };

describe("projectFormOf et projectRequestOf", () => {
  it("renvoient au serveur le projet lu, sa page comprise, sans l'adresse complète ni les dates", () => {
    expect(projectRequestOf(projectFormOf(published))).toEqual({
      name: "client-a",
      title: "Client A",
      publication: { slug: "client-a", announcement: "Travaux jeudi", language: "en" },
    });
  });

  it("n'envoient pas de page pour un projet non publié", () => {
    expect(projectFormOf(draft).published).toBe(false);
    expect(projectRequestOf(projectFormOf(draft))).toEqual({ name: "brouillon", title: "", publication: null });
  });

  it("retirent la page d'un projet dépublié, même si l'adresse reste dans le formulaire", () => {
    const form = { ...projectFormOf(published), published: false };
    expect(projectRequestOf(form).publication).toBeNull();
  });

  it("partent d'un projet non publié, dans la langue de l'interface", () => {
    expect(emptyProjectForm("fr")).toEqual({ name: "", title: "", published: false, slug: "", announcement: "", language: "fr" });
  });
});

describe("slugSuggestionOf", () => {
  it("change les tirets bas en tirets : un slug n'en admet pas", () => {
    expect(slugSuggestionOf(" client_a ")).toBe("client-a");
    expect(slugSuggestionOf("cloud")).toBe("cloud");
  });
});

describe("projectLabel et publicAddressOf", () => {
  it("nomment le projet par son titre, sinon son nom, et la page par son chemin", () => {
    expect(projectLabel(published)).toBe("Client A");
    expect(projectLabel(draft)).toBe("brouillon");
    expect(publicAddressOf("client-a")).toBe("/statut/client-a");
  });
});

describe("chosenProjectID", () => {
  const projects = [{ id: "a" }, { id: "b" }];
  it("garde le projet demandé s'il existe", () => {
    expect(chosenProjectID(projects, "b")).toBe("b");
  });
  it("prend le premier sinon, et aucun sans projet", () => {
    expect(chosenProjectID(projects, "disparu")).toBe("a");
    expect(chosenProjectID(projects, null)).toBe("a");
    expect(chosenProjectID([], "a")).toBeNull();
  });
});

describe("projectsSubtitle", () => {
  const t = (key: string, ...args: (string | number)[]) => [key, ...args].join(" ");
  it("dit aucun, un, ou le compte", () => {
    expect(projectsSubtitle(t, 0)).toBe("project.subtitle_none");
    expect(projectsSubtitle(t, 1)).toBe("project.subtitle_one");
    expect(projectsSubtitle(t, 4)).toBe("project.subtitle 4");
  });
});
