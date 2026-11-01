import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router";
import { expect, test, vi } from "vitest";

import type { Project } from "../api/directory";
import { ComponentForm } from "./StatusComponentForm";

vi.mock("../i18n/context", () => ({ useT: () => (key: string) => key }));

function projectOf(id: string, name: string, title: string): Project {
  const publication = { slug: name, announcement: "", language: "fr", public_url: "", published_at: "" };
  return { id, name, title, service_ids: [], publication, created_at: "", updated_at: "" };
}

// L'ordre du serveur (par nom) met « interne » avant le projet demandé.
const projects = [projectOf("prj-interne", "interne", "Interne"), projectOf("prj-etat", "services", "État des services")];

test("à son premier rendu, le formulaire d'un composant montre le projet demandé", () => {
  const markup = renderToStaticMarkup(
    <MemoryRouter>
      <ComponentForm existing={null} projectID="prj-etat" projects={projects} />
    </MemoryRouter>,
  );
  expect(markup).toContain('id="component-project"');
  expect(markup).toContain(">État des services<");
});
